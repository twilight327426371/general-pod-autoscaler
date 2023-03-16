// Copyright 2021 The OCGI Authors.
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

package validation

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/ocgi/general-pod-autoscaler/pkg/apis/autoscaling/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type TestCronSchedule struct {
	name    string
	mode    v1alpha1.CronMetricMode
	desired int32
	gpa     *v1alpha1.GeneralPodAutoscaler
	time    time.Time
}

func intPtr(v int32) *int32 {
	return &v
}

// TestValidationCronFirst "* 20-22 * * 0" and  "* 20-21 * * 0" conflict but Priority not same, so no error
func TestValidationCronFirst(t *testing.T) {
	var cpuUtilization int32 = 30
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 * * 0",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "* 20-21 * * 0",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    50,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				def,
			},
		},
	}
	var minReplicasLowerBound int32
	fldPath := field.NewPath("spec")
	t.Run(tc.name, func(t *testing.T) {
		errList := validateCronMetric(&tc.mode, fldPath.Child("cronMetric"), minReplicasLowerBound)
		t.Logf("get validation err: %v", errList)
		if len(errList) >= 1 {
			t.Errorf("desired has err, actual err: %v", errList)
		}
	})
}

// TestValidationCronTwo "* 20-22 * * *" and   "* 20-21 * * *" has conflict and Priority same
func TestValidationCronTwo(t *testing.T) {
	var cpuUtilization int32 = 30
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "* 20-21 * * *",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				def,
			},
		},
	}
	var minReplicasLowerBound int32
	fldPath := field.NewPath("spec")
	t.Run(tc.name, func(t *testing.T) {
		errList := validateCronMetric(&tc.mode, fldPath.Child("cronMetric"), minReplicasLowerBound)
		t.Logf("get validation err: %v", errList)
		// has conflict, must with error
		if len(errList) < 1 {
			t.Errorf("desired no err, actual no err lenth: %d", len(errList))
		}
	})
}

// TestValidationCronThree "* 20-22 1 10 * 2023" and  "* 20-21 1 10 * 2023" conflict with hour and Priority 100
func TestValidationCronThree(t *testing.T) {
	var cpuUtilization int32 = 30
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "* 20-21 1 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				def,
			},
		},
	}
	var minReplicasLowerBound int32
	fldPath := field.NewPath("spec")
	t.Run(tc.name, func(t *testing.T) {
		errList := validateCronMetric(&tc.mode, fldPath.Child("cronMetric"), minReplicasLowerBound)
		t.Logf("get validation err: %v", errList)
		// has conflict, must with error
		if len(errList) < 1 {
			t.Errorf("desired has err, actual no err lenth: %d", len(errList))
		}
	})
}

// TestValidationCronFour "* 20-22 1,2,3 10 * 2023" and  "* 20-21 3,4,5 10 * 2023"
func TestValidationCronFour(t *testing.T) {
	var cpuUtilization int32 = 30
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "* 20-21 2 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				def,
			},
		},
	}
	var minReplicasLowerBound int32
	fldPath := field.NewPath("spec")
	t.Run(tc.name, func(t *testing.T) {
		errList := validateCronMetric(&tc.mode, fldPath.Child("cronMetric"), minReplicasLowerBound)
		t.Logf("get validation err: %v", errList)
		// has conflict, must with error
		if len(errList) >= 1 {
			t.Errorf("desired no err, actual no err lenth: %d", len(errList))
		}
	})
}

// TestValidationCronFive "* 20-22 1 10 * 2023" and  "* 20-21 2 10 * 2023"
func TestValidationCronFive(t *testing.T) {
	var cpuUtilization int32 = 30
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1,2,3 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "* 20-21 3,4,5 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    100,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
					MetricSpec: v1alpha1.MetricSpec{
						Type: v1alpha1.ContainerResourceMetricSourceType,
						ContainerResource: &v1alpha1.ContainerResourceMetricSource{
							Name: v1.ResourceCPU,
							Target: v1alpha1.MetricTarget{
								Type:               v1alpha1.UtilizationMetricType,
								AverageUtilization: &cpuUtilization,
							},
							Container: "container",
						},
					},
				},
				def,
			},
		},
	}
	var minReplicasLowerBound int32
	fldPath := field.NewPath("spec")
	t.Run(tc.name, func(t *testing.T) {
		errList := validateCronMetric(&tc.mode, fldPath.Child("cronMetric"), minReplicasLowerBound)
		t.Logf("get validation err: %v", errList)
		// has conflict, must with error
		if len(errList) < 1 {
			t.Errorf("desired has err, actual no err lenth: %d", len(errList))
		}
	})
}
