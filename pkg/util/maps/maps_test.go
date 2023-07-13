/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package maps

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestToRefMap(t *testing.T) {
	cases := map[string]struct {
		orig       map[int]int
		updatesMap map[int]int
	}{
		"preserve nil": {
			orig: nil,
		},
		"preserve empty": {
			orig: map[int]int{},
		},
		"slice": {
			orig: map[int]int{
				1: 0xa,
				2: 0xb,
				3: 0xc,
			},
			updatesMap: map[int]int{
				1: 0xd,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := Clone(tc.orig)
			if diff := cmp.Diff(tc.orig, result); diff != "" {
				t.Errorf("Unexpected result (-want,+got):\n%s", diff)
			}

			if len(tc.updatesMap) > 0 {
				for k, v := range tc.updatesMap {
					result[k] = v
				}
				if diff := cmp.Diff(tc.orig, result); diff == "" {
					t.Errorf("changing the result should not alter the original")
				}
			}
		})

	}
}

func TestContains(t *testing.T) {
	cases := map[string]struct {
		a          map[string]int
		b          map[string]int
		wantResult bool
	}{
		"nil a": {
			a: nil,
			b: map[string]int{
				"v1": 1,
			},
			wantResult: false,
		},
		"nil b": {
			a: map[string]int{
				"v1": 1,
			},
			b:          nil,
			wantResult: true,
		},
		"extra in b": {
			a: map[string]int{
				"v1": 1,
			},
			b: map[string]int{
				"v1": 1,
				"v2": 2,
			},
			wantResult: false,
		},
		"extra in a": {
			a: map[string]int{
				"v1": 1,
				"v2": 2,
			},
			b: map[string]int{
				"v1": 1,
			},
			wantResult: true,
		},
		"missmatch": {
			a: map[string]int{
				"v1": 1,
				"v2": 3,
			},
			b: map[string]int{
				"v1": 1,
				"v2": 2,
			},
			wantResult: false,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := Contains(tc.a, tc.b)
			if got != tc.wantResult {
				t.Errorf("Unexpected result, expecting %v", tc.wantResult)
			}
		})
	}
}
