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
    - name: baz1
      sourcePath: examples/source-packages/pkg3
      metadata:
        spec:
          k3-2: v3-2
          k4-2: v4-2`,
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
	a.Equal(t, map[string]string{"name": "baz1", "k1": "v1", "k2": "v2", "k3-2": "v3-2", "k4-2": "v4-2"}, f.Spec.Packages[1].Packages[0].Metadata.mergedSpec, "calculated metadata")
}
