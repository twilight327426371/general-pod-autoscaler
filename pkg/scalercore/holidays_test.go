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

package scalercore

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ocgi/general-pod-autoscaler/pkg/apis/autoscaling/v1alpha1"
)

type TestHolidaysSchedule struct {
	name    string
	mode    v1alpha1.CronMetricMode
	desired int32
	gpa     *v1alpha1.GeneralPodAutoscaler
	time    time.Time
}

func intPtr(v int32) *int32 {
	return &v
}

// TestInHolidaysScheduleFirst 2023 10 1 20:00:01 in `* 20-22 1 10 * 2023`
func TestInHolidaysScheduleFirst(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 00, 01, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 5 || actualMax != 7 {
			t.Errorf("desired min: 5, max: 7, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 1 10 * 2023" {
			t.Errorf("desired schedule: `* 20-22 1 10 * 2023`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleTwo 2024 10 1 20:00:01 not in `* 20-22 1 10 * 2023`
func TestInHolidaysScheduleTwo(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2024, 10, 1, 20, 00, 01, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 9 || actualMax != 10 {
			t.Errorf("desired min: 9, max: 10, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "default" {
			t.Errorf("desired schedule: `default`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleThree 2023 10 1 20:00:01 in `* 20-22 1 10 * 2023` priority 100, not in `* 20-22 1 10 * 0` 50
func TestInHolidaysScheduleThree(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 00, 01, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 * * 0",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    50,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 5 || actualMax != 7 {
			t.Errorf("desired min: 5, max: 7, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 1 10 * 2023" {
			t.Errorf("desired schedule: `* 20-22 1 10 * 2023`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleFour 2023 10 1 20:00:01 not in `* 20-22 1 10 * 2023` priority 100, in `* 20-22 1 10 * 0` 200
func TestInHolidaysScheduleFour(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 00, 01, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 1 10 *",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 12 || actualMax != 13 {
			t.Errorf("desired min: 12, max: 13, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 1 10 *" {
			t.Errorf("desired schedule: `* 20-22 1 10 0`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleFive 2023 10 1 20:00:00 not in `* 20-22 1 10 * 2023` priority 100, in `* 20-22 1 10 * 0` 200
func TestInHolidaysScheduleFive(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 00, 00, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 * * 0",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 12 || actualMax != 13 {
			t.Errorf("desired min: 12, max: 13, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 * * 0" {
			t.Errorf("desired schedule: `* 20-22 * * 0`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleSix 2023 10 1 19:59:02 not in `* 20-22 1 10 * 2023` priority 100, not in `* 20-22 1 10 * 0` 200
// in `default`
func TestInHolidaysScheduleSix(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 19, 59, 02, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 1 10 0",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 9 || actualMax != 10 {
			t.Errorf("desired min: 9, max: 10, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "default" {
			t.Errorf("desired schedule: `default`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleSeven 2023 10 1 19:59:02 in `* 20-22 1 10 * 2023` priority 100, in `* 20-22 1 10 * 0` priority 200
// compare priority,so choose `* 20-22 1 10 * 0`
func TestInHolidaysScheduleSeven(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 59, 02, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1,2,3 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 1 10 0",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 12 || actualMax != 13 {
			t.Errorf("desired min: 12, max: 13, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 1 10 0" {
			t.Errorf("desired schedule: `* 20-22 1 10 0`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleEighth 2023 10 1 19:59:02 in `* 20-22 1 10 * 2023` priority 100, not in `* 20-22 4,5,6 10 * 2023` priority 200
func TestInHolidaysScheduleEighth(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 59, 02, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1,2,3 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 4,5,6 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 5 || actualMax != 7 {
			t.Errorf("desired min: 12, max: 13, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 1,2,3 10 * 2023" {
			t.Errorf("desired schedule: `* 20-22 1,2,3 10 * 2023`, actual schedule: %v", schedule)
		}
	})
}

// TestInHolidaysScheduleNinth 2023 10 1 19:59:02 in `* 20-22 1 10 * 2023` priority 100, not in `* 20-22 4,5,6 10 * 2023` priority 200
func TestInHolidaysScheduleNinth(t *testing.T) {
	t1 := time.Now()
	testTime1 := time.Date(2023, 10, 1, 20, 59, 02, 0, t1.Location())
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{},
	}
	def := v1alpha1.CronMetricSpec{
		Schedule:    "default",
		MinReplicas: intPtr(9),
		MaxReplicas: 10,
	}
	tc := TestHolidaysSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "* 20-22 1,2,3 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 4,5,6 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				{
					Schedule:    "* 20-22 7,8,9 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "0-30 23 7,8,9 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 23 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				{
					Schedule:    "* 13-14 1,2,3 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 20-22 4,5,6 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				{
					Schedule:    "* 15-16 1,2,3 10 * 2023",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
					Priority:    100,
				},
				{
					Schedule:    "* 17-22 4,5,6 10 * 2023",
					MinReplicas: intPtr(12),
					MaxReplicas: 13,
					Priority:    200,
				},
				{
					Schedule:    "20-59 20-22 30 9 * 2023",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
					Priority:    50,
				},
				def,
			},
		},
	}
	t.Run(tc.name, func(t *testing.T) {
		defaultGPA := gpa
		if tc.gpa != nil {
			defaultGPA = tc.gpa
		}
		testTime := testTime1
		if !tc.time.IsZero() {
			testTime = tc.time
		}
		cron := &CronMetricsScaler{ranges: tc.mode.CronMetrics, name: Cron, now: testTime, defaultSet: def}
		actualMax, actualMin, schedule := cron.GetCurrentMaxAndMinReplicas(defaultGPA)
		if actualMin != 5 || actualMax != 7 {
			t.Errorf("desired min: 12, max: 13, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "* 20-22 1,2,3 10 * 2023" {
			t.Errorf("desired schedule: `* 20-22 1,2,3 10 * 2023`, actual schedule: %v", schedule)
		}
	})
}
