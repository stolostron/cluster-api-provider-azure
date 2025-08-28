/*
Copyright 2025 The Kubernetes Authors.

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

package hcpopenshiftclustercredentials

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
)

func TestParameters(t *testing.T) {
	testcases := []struct {
		name     string
		spec     HcpOpenShiftClusterCredentialsSpec
		existing interface{}
		expected interface{}
		errorMsg string
	}{
		{
			name:     "returns spec parameters",
			spec:     fakeCredentialsSpec(),
			existing: nil,
			expected: fakeCredentialsSpecPointer(),
		},
		{
			name:     "returns spec parameters with existing (ignored)",
			spec:     fakeCredentialsSpec(),
			existing: "some existing data",
			expected: fakeCredentialsSpecPointer(),
		},
		{
			name: "returns spec parameters with different values",
			spec: HcpOpenShiftClusterCredentialsSpec{
				Name:          "different-cluster",
				ResourceGroup: "different-rg",
				APIURI:        "https://different.example.com",
			},
			existing: nil,
			expected: &HcpOpenShiftClusterCredentialsSpec{
				Name:          "different-cluster",
				ResourceGroup: "different-rg",
				APIURI:        "https://different.example.com",
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Parallel()

			result, err := tc.spec.Parameters(t.Context(), tc.existing)
			if tc.errorMsg != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.errorMsg))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("Got difference between expected result and computed result:\n%s", cmp.Diff(tc.expected, result))
			}
		})
	}
}

func TestResourceName(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()

	spec := fakeCredentialsSpec()
	result := spec.ResourceName()
	g.Expect(result).To(Equal("test-cluster"))
}

func TestResourceGroupName(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()

	spec := fakeCredentialsSpec()
	result := spec.ResourceGroupName()
	g.Expect(result).To(Equal("test-rg"))
}

func TestOwnerResourceName(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()

	spec := fakeCredentialsSpec()
	result := spec.OwnerResourceName()
	g.Expect(result).To(Equal("test-cluster"))
}

func fakeCredentialsSpec() HcpOpenShiftClusterCredentialsSpec {
	return HcpOpenShiftClusterCredentialsSpec{
		Name:          "test-cluster",
		ResourceGroup: "test-rg",
		APIURI:        "https://api.test-cluster.example.com",
	}
}

func fakeCredentialsSpecPointer() *HcpOpenShiftClusterCredentialsSpec {
	spec := fakeCredentialsSpec()
	return &spec
}
