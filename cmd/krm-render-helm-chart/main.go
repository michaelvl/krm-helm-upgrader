// Copyright 2023 Michael Vittrup Larsen
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/GoogleContainerTools/kpt-functions-sdk/go/fn"
	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/michaelvl/krm-functions/pkg/helm"
	t "github.com/michaelvl/krm-functions/pkg/helmspecs"
	"sigs.k8s.io/kustomize/kyaml/kio"
	kyaml "sigs.k8s.io/kustomize/kyaml/yaml"
)

const (
	annotationURL              = "experimental.helm.sh/"
	annotationShaSum           = annotationURL + "chart-sum"
	maxChartTemplateFileLength = 1024 * 1024
)

func Run(rl *fn.ResourceList) (bool, error) {
	var outputs fn.KubeObjects

	for _, kubeObject := range rl.Items {
		switch {
		case kubeObject.IsGVK("experimental.helm.sh", "", "RenderHelmChart"):
			y := kubeObject.String()
			spec, err := t.ParseKptSpec([]byte(y))
			if err != nil {
				return false, err
			}
			for idx := range spec.Charts {
				if spec.Charts[idx].Options.ReleaseName == "" {
					return false, fmt.Errorf("invalid chart spec %s: ReleaseName required, index %d", kubeObject.GetName(), idx)
				}
			}
			for idx := range spec.Charts {
				newobjs, err := Template(&spec.Charts[idx])
				if err != nil {
					return false, err
				}
				outputs = append(outputs, newobjs...)
			}
		case kubeObject.IsGVK("fn.kpt.dev", "", "RenderHelmChart"):
			y := kubeObject.String()
			spec, err := t.ParseKptSpec([]byte(y))
			if err != nil {
				return false, err
			}
			for idx := range spec.Charts {
				chart := &spec.Charts[idx]
				var uname, pword *string
				if chart.Args.Auth != nil {
					uname, pword, err = helm.LookupAuthSecret(chart.Args.Auth.Name, chart.Args.Auth.Namespace, rl)
					if err != nil {
						return false, err
					}
				}
				chartData, chartSum, err := SourceChart(chart, uname, pword)
				if err != nil {
					return false, err
				}
				err = kubeObject.SetAPIVersion("experimental.helm.sh/v1alpha1")
				if err != nil {
					return false, err
				}
				chs, found, err := kubeObject.NestedSlice("helmCharts")
				if !found {
					return false, fmt.Errorf("helmCharts key not found in %s", kubeObject.GetName())
				}
				if err != nil {
					return false, err
				}
				err = chs[0].SetNestedField(base64.StdEncoding.EncodeToString(chartData), "chart")
				if err != nil {
					return false, err
				}
				err = kubeObject.SetAnnotation(annotationShaSum, "sha256:"+chartSum)
				if err != nil {
					return false, err
				}
				outputs = append(outputs, kubeObject)
			}
		default:
			outputs = append(outputs, kubeObject)
		}
	}

	rl.Items = outputs
	return true, nil
}

func SourceChart(chart *t.HelmChart, username, password *string) (chartData []byte, chartSha256Sum string, err error) {
	tmpDir, err := os.MkdirTemp("", "chart-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(tmpDir)

	tarball, chartSum, err := helm.PullChart(chart.Args, tmpDir, username, password)
	if err != nil {
		return nil, "", err
	}
	buf, err := os.ReadFile(filepath.Join(tmpDir, tarball))
	if err != nil {
		return nil, "", err
	}
	return buf, chartSum, err
}

func Template(chart *t.HelmChart) (fn.KubeObjects, error) {
	chartfile, err := base64.StdEncoding.DecodeString(chart.Chart)
	if err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "chart-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	gzr, err := gzip.NewReader(bytes.NewReader(chartfile))
	if err != nil {
		return nil, err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)

	// Extract tar archive files
	for {
		hdr, xtErr := tr.Next()
		if xtErr == io.EOF {
			break // End of archive
		} else if xtErr != nil {
			return nil, xtErr
		}
		fname := hdr.Name
		if path.IsAbs(fname) {
			return nil, errors.New("chart contains file with absolute path")
		}
		fileWithPath, fnerr := securejoin.SecureJoin(tmpDir, fname)
		if fnerr != nil {
			return nil, fnerr
		}
		if hdr.Typeflag == tar.TypeReg {
			fdir := filepath.Dir(fileWithPath)
			if mkdErr := os.MkdirAll(fdir, 0o755); mkdErr != nil {
				return nil, mkdErr
			}

			file, fErr := os.OpenFile(fileWithPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if fErr != nil {
				return nil, fErr
			}
			_, fErr = io.CopyN(file, tr, maxChartTemplateFileLength)
			file.Close()
			if fErr != nil && fErr != io.EOF {
				return nil, fErr
			}
		}
	}

	valuesFile := filepath.Join(tmpDir, "values.yaml")
	err = writeValuesFile(chart, valuesFile)
	if err != nil {
		return nil, err
	}
	args := buildHelmTemplateArgs(chart)
	args = append(args, "--values", valuesFile, filepath.Join(tmpDir, chart.Args.Name))

	helmCtxt := helm.NewRunContext()
	defer helmCtxt.DiscardContext()
	stdout, err := helmCtxt.Run(args...)
	if err != nil {
		return nil, err
	}

	r := &kio.ByteReader{Reader: bytes.NewBufferString(string(stdout)), OmitReaderAnnotations: true}
	nodes, err := r.Read()
	if err != nil {
		return nil, err
	}

	var objects fn.KubeObjects
	for i := range nodes {
		o, parseErr := fn.ParseKubeObject([]byte(nodes[i].MustString()))
		if parseErr != nil {
			if strings.Contains(parseErr.Error(), "expected exactly one object, got 0") {
				continue
			}
			return nil, fmt.Errorf("failed to parse %s: %s", nodes[i].MustString(), parseErr.Error())
		}
		objects = append(objects, o)
	}

	if err != nil {
		return nil, err
	}

	return objects, nil
}

// Write embedded values to a file for passing to Helm
func writeValuesFile(chart *t.HelmChart, valuesFilename string) error {
	vals := chart.Options.Values.ValuesInline
	b, err := kyaml.Marshal(vals)
	if err != nil {
		return err
	}
	return os.WriteFile(valuesFilename, b, 0o600)
}

func buildHelmTemplateArgs(chart *t.HelmChart) []string {
	opts := chart.Options
	args := []string{"template"}
	if opts.ReleaseName != "" {
		args = append(args, opts.ReleaseName)
	}
	if opts.Namespace != "" {
		args = append(args, "--namespace", opts.Namespace)
	}
	if opts.NameTemplate != "" {
		args = append(args, "--name-template", opts.NameTemplate)
	}
	for _, apiVer := range opts.APIVersions {
		args = append(args, "--api-versions", apiVer)
	}
	if opts.Description != "" {
		args = append(args, "--description", opts.Description)
	}
	if opts.IncludeCRDs {
		args = append(args, "--include-crds")
	}
	if opts.SkipTests {
		args = append(args, "--skip-tests")
	}
	return args
}

func main() {
	if err := fn.AsMain(fn.ResourceListProcessorFunc(Run)); err != nil {
		os.Exit(1)
	}
}
