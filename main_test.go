/*
Copyright 2026 The Kubernetes Authors.

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

package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestAROHcpOpenShiftASOAPIVersionsRegistered(t *testing.T) {
	for _, version := range []string{"v20260901preview", "v20261001"} {
		for _, kind := range []string{
			"HcpOpenShiftCluster",
			"HcpOpenShiftClustersNodePool",
			"HcpOpenShiftClustersExternalAuth",
		} {
			for _, registeredVersion := range []string{version, version + "storage"} {
				gvk := schema.GroupVersionKind{
					Group:   "redhatopenshift.azure.com",
					Version: registeredVersion,
					Kind:    kind,
				}
				t.Run(registeredVersion+"/"+kind, func(t *testing.T) {
					if _, err := scheme.New(gvk); err != nil {
						t.Fatalf("expected %s to be registered: %v", gvk, err)
					}
				})
			}
		}
	}
}
