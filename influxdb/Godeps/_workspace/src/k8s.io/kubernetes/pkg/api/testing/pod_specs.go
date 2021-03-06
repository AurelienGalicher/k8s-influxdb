/*
Copyright 2015 The Kubernetes Authors All rights reserved.

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

package testing

import (
	"revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/k8s.io/kubernetes/pkg/api"
)

// DeepEqualSafePodSpec returns a PodSpec which is ready to be used with api.Semantic.DeepEqual
func DeepEqualSafePodSpec() api.PodSpec {
	grace := int64(30)
	return api.PodSpec{
		RestartPolicy:                 api.RestartPolicyAlways,
		DNSPolicy:                     api.DNSClusterFirst,
		TerminationGracePeriodSeconds: &grace,
		SecurityContext:               &api.PodSecurityContext{},
	}
}
