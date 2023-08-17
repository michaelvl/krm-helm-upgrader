// Copyright 2023 Michael Vittrup Larsen

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at

//     http://www.apache.org/licenses/LICENSE-2.0

// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/michaelvl/krm-functions/pkg/helm"
	t "github.com/michaelvl/krm-functions/pkg/helmspecs"
	"github.com/michaelvl/krm-functions/pkg/semver"
	"github.com/michaelvl/krm-functions/pkg/version"

	"github.com/GoogleContainerTools/kpt-functions-sdk/go/fn"
)

const annotationURL string = "experimental.helm.sh/"
const annotationUpgradeConstraint string = annotationURL + "upgrade-constraint"
const annotationUpgradeAvailable string = annotationURL + "upgrade-available"
const annotationShaSum string = annotationURL + "chart-sum"
const annotationUpgradeShaSum string = annotationURL + "upgrade-chart-sum"

var upgradesDone, upgradesAvailable int

// Lookup versions and find a possible upgrade that fulfils constraints
func evaluateChartVersion(chart t.HelmChartArgs, upgradeConstraint string) (*t.HelmChartArgs, error) {
	if upgradeConstraint == "" {
		upgradeConstraint = "*"
	}
	search, err := helm.SearchRepo(chart, nil, nil)
	if err != nil {
		return nil, err
	}
	search = helm.FilterByChartName(search, chart)
	versions := helm.ToList(search)
	newVersion, err := semver.Upgrade(versions, upgradeConstraint)
	if err != nil {
		return nil, err
	}

	newChart := chart
	newChart.Version = newVersion
	return &newChart, nil
}

// Apply new version to chart spec
func handleNewVersion(newChart t.HelmChartArgs, curr t.HelmChartArgs, kubeObject *fn.KubeObject, idx int, upgradeConstraint string) (*t.HelmChartArgs, string, error) {
	upgraded := curr
	var info string
	if newChart.Version != curr.Version {
		upgradesAvailable++
		anno := curr.Repo + "/" + curr.Name + ":" + newChart.Version
		if Config.AnnotateOnUpgradeAvailable {
			if idx >= 0 {
				err := kubeObject.SetAnnotation(annotationUpgradeAvailable+"."+strconv.FormatInt(int64(idx), 10), anno)
				if err != nil {
					return nil, "", err
				}
			} else {
				err := kubeObject.SetAnnotation(annotationUpgradeAvailable, anno)
				if err != nil {
					return nil, "", err
				}
			}
		}
		if Config.UpgradeOnUpgradeAvailable {
			upgradesDone++
			upgraded.Version = newChart.Version
		}
		if Config.AnnotateSumOnUpgradeAvailable {
			_, chartSum, err := helm.PullChart(newChart, "", nil, nil)
			if err != nil {
				return nil, "", err
			}
			if idx >= 0 {
				err = kubeObject.SetAnnotation(annotationUpgradeShaSum+"."+strconv.FormatInt(int64(idx), 10), "sha256:"+chartSum)
				if err != nil {
					return nil, "", err
				}
			} else {
				err = kubeObject.SetAnnotation(annotationUpgradeShaSum, "sha256:"+chartSum)
				if err != nil {
					return nil, "", err
				}
			}
		}
		upgradedJSON, _ := json.Marshal(upgraded)
		currJSON, _ := json.Marshal(curr)
		info = fmt.Sprintf("{\"current\": %s, \"upgraded\": %s, \"constraint\": %q}\n", string(currJSON), string(upgradedJSON), upgradeConstraint)
	} else {
		if Config.AnnotateCurrentSum && kubeObject.GetAnnotation(annotationShaSum) == "" {
			_, chartSum, err := helm.PullChart(curr, "", nil, nil)
			if err != nil {
				return nil, "", err
			}
			err = kubeObject.SetAnnotation(annotationShaSum, "sha256:"+chartSum)
			if err != nil {
				return nil, "", err
			}
		}
	}
	return &upgraded, info, nil
}

func Run(rl *fn.ResourceList) (bool, error) {
	cfg := rl.FunctionConfig
	parseConfig(cfg)
	results := &rl.Results

	for _, kubeObject := range rl.Items {
		if kubeObject.IsGVK("fn.kpt.dev", "", "RenderHelmChart") || kubeObject.IsGVK("experimental.helm.sh", "", "RenderHelmChart") {
			upgradeConstraint := kubeObject.GetAnnotation(annotationUpgradeConstraint)

			y := kubeObject.String()
			spec, err := t.ParseKptSpec([]byte(y))
			if err != nil {
				return false, err
			}
			for idx := range spec.Charts {
				helmChart := &spec.Charts[idx]
				newVersion, err := evaluateChartVersion(helmChart.Args, upgradeConstraint)
				if err != nil {
					return false, err
				}
				upgraded, info, err := handleNewVersion(*newVersion, helmChart.Args, kubeObject, idx, upgradeConstraint)
				if err != nil {
					return false, err
				}
				helmChart.Args.Version = upgraded.Version
				*results = append(*results, fn.ConfigObjectResult(info, kubeObject, fn.Info))
			}
			err = kubeObject.SetNestedField(spec.Charts, "helmCharts")
			if err != nil {
				return false, err
			}
		} else if kubeObject.IsGVK("argoproj.io", "", "Application") {
			upgradeConstraint := kubeObject.GetAnnotation(annotationUpgradeConstraint)

			y := kubeObject.String()
			app, err := t.ParseArgoCDSpec([]byte(y))
			if err != nil {
				return false, err
			}
			chartArgs := app.Spec.Source.ToKptSpec()
			newVersion, err := evaluateChartVersion(chartArgs, upgradeConstraint)
			if err != nil {
				return false, err
			}
			upgraded, info, err := handleNewVersion(*newVersion, chartArgs, kubeObject, -1, upgradeConstraint)
			if err != nil {
				return false, err
			}
			*results = append(*results, fn.ConfigObjectResult(info, kubeObject, fn.Info))
			err = kubeObject.SetNestedField(upgraded.Version, "spec", "source", "targetRevision")
			if err != nil {
				return false, err
			}
		}
	}

	*results = append(*results, fn.GeneralResult(fmt.Sprintf("{\"upgradesDone\": %d, \"upgradesAvailable\": %d, \"upgradesSkipped\": %d}\n", upgradesDone, upgradesAvailable, upgradesAvailable-upgradesDone),fn.Info))
	return true, nil
}

func main() {
	fmt.Fprintf(os.Stderr, "version: %s\n", version.Version)
	if err := fn.AsMain(fn.ResourceListProcessorFunc(Run)); err != nil {
		os.Exit(1)
	}
}
