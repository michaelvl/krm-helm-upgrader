// Copyright 2024 Michael Vittrup Larsen
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"testing"

	a "github.com/stretchr/testify/assert"
)

var fleetMustParse = []string{`apiVersion: fn.kpt.dev/v1alpha1
kind: Fleet
metadata:
  name: example-fleet
spec:
  upstreams:
  - name: example
    type: git
    git:
      repo: https://github.com/krm-functions/catalog.git
  defaults:
    ref: main
    metadata:
      spec:
        k1: v1
        k2: v2
  packages:
  - name: foo
    sourcePath: examples/source-packages/pkg1
  - name: bar
    sourcePath: examples/source-packages/pkg2
    metadata:
      spec:
        k2: v2
        k3: v3
    packages:
    - name: bar1
      sourcePath: examples/source-packages/pkg3
      metadata:
        spec:
          k3-2: v3-2
          k4-2: v4-2
  - name: zap
    stub: true
    metadata:
      spec:
        k4: v4
        k5: v5
    packages:
    - name: zap1
      sourcePath: examples/source-packages/pkg4
      metadata:
        spec:
          k5-2: v5-2
          k6-2: v6-2
    - name: zap2
      sourcePath: examples/source-packages/pkg4
      metadata:
        inheritFromParent: false
        spec:
          k7: v7
        templated:
          k8: "{{.name}}"`,
}

var fleetMustFailParse = []string{
	`xxx`,
	// Missing ref in foo
	`apiVersion: fn.kpt.dev/v1alpha1
kind: Fleet
metadata:
  name: example-fleet
spec:
  upstreams:
  - name: example
    type: git
    git:
      repo: https://github.com/krm-functions/catalog.git
  packages:
  - name: foo
    sourcePath: examples/source-packages/pkg1
`,
	// Defaults cannot hanve metadata 'name'
	`apiVersion: fn.kpt.dev/v1alpha1
kind: Fleet
metadata:
  name: example-fleet
spec:
  upstreams:
  - name: example
    type: git
    git:
      repo: https://github.com/krm-functions/catalog.git
  defaults:
    metadata:
      spec:
        name: cannot-have-name-key
  packages:
  - name: foo
    sourcePath: examples/source-packages/pkg1
    ref: main
`,
}

func TestFleetParsing(t *testing.T) {
	for _, fltfile := range fleetMustParse {
		f, err := ParseFleetSpec([]byte(fltfile))
		if f == nil || err != nil {
			t.Fatalf(`Expected Fleet spec to parse: %v`, err)
		}
	}
	for _, fltfile := range fleetMustFailParse {
		f, err := ParseFleetSpec([]byte(fltfile))
		if f != nil || err == nil {
			t.Fatalf(`Expected Fleet spec parsing to fail: %v`, fltfile)
		}
	}
}

func TestMetadataPropagation(t *testing.T) {
	f, err := ParseFleetSpec([]byte(fleetMustParse[0]))
	if f == nil || err != nil {
		t.Fatalf(`Expected Fleet spec to parse: %v`, err)
	}

	a.Equal(t, map[string]string{"name": "foo", "k1": "v1", "k2": "v2"}, f.Spec.Packages[0].Metadata.mergedSpec, "calculated metadata")
	a.Equal(t, map[string]string{"name": "bar", "k1": "v1", "k2": "v2", "k3": "v3"}, f.Spec.Packages[1].Metadata.mergedSpec, "calculated metadata")
	a.Equal(t, map[string]string{"name": "bar1", "k1": "v1", "k2": "v2", "k3": "v3", "k3-2": "v3-2", "k4-2": "v4-2"}, f.Spec.Packages[1].Packages[0].Metadata.mergedSpec, "calculated metadata")
	a.Equal(t, map[string]string{"name": "zap1", "k1": "v1", "k2": "v2", "k4": "v4", "k5": "v5", "k5-2": "v5-2", "k6-2": "v6-2"}, f.Spec.Packages[2].Packages[0].Metadata.mergedSpec, "calculated metadata")
	a.Equal(t, map[string]string{"name": "zap2", "k7": "v7", "k8": "zap2"}, f.Spec.Packages[2].Packages[1].Metadata.mergedSpec, "calculated metadata")
}
