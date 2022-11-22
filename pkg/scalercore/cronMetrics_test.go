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

func TestInCronScheduleFirst(t *testing.T) {
	//testTime1, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-11-22 09:00:01", time.Local)
	//if err != nil {
	//	t.Fatal(err)
	//}
	t1 := time.Now()
	testTime1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 9, 00, 01, 0, t1.Location())
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
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "5-59 10-12 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
				},
				{
					Schedule:    "0-4 9-10 * * *",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
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
		if actualMin != 6 || actualMax != 8 {
			t.Errorf("desired min: 6, max: 8, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "0-4 9-10 * * *" {
			t.Errorf("desired schedule: `0-4 9-10 * * *`, actual schedule: %v", schedule)
		}
	})
}

func TestInCronScheduleSecond(t *testing.T) {
	//testTime1, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-11-22 09:04:41", time.Local)
	//if err != nil {
	//	t.Fatal(err)
	//}
	t1 := time.Now()
	testTime1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 9, 04, 41, 0, t1.Location())
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
	tc := TestCronSchedule{
		name: "single timeRange, out of range",
		mode: v1alpha1.CronMetricMode{
			CronMetrics: []v1alpha1.CronMetricSpec{
				{
					Schedule:    "5-59 10-12 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
				},
				{
					Schedule:    "0-4 9-10 * * *",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
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
		if actualMin != 6 || actualMax != 8 {
			t.Errorf("desired min: 6, max: 8, actual min: %v, max: %v", actualMin, actualMax)
		}
		if schedule != "0-4 9-10 * * *" {
			t.Errorf("desired schedule: `0-4 9-10 * * *`, actual schedule: %v", schedule)
		}
	})
}

func TestInCronScheduleThird(t *testing.T) {
	//testTime1, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-11-22 13:04:41", time.Local)
	//if err != nil {
	//	t.Fatal(err)
	//}
	t1 := time.Now()
	testTime1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 13, 04, 41, 0, t1.Location())
	lastTime := metav1.Time{Time: testTime1.Add(-1 * time.Second)}
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{
			LastCronScheduleTime: &lastTime,
		},
	}
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
					Schedule:    "0-4 10-12 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
				},
				{
					Schedule:    "0-59 9-10 * * *",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
				},
				{
					Schedule:    "0-4 13-14 * * *",
					MinReplicas: intPtr(11),
					MaxReplicas: 12,
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
		if schedule != "0-4 13-14 * * *" {
			t.Errorf("desired schedule: `0-4 13-14 * * *`, actual schedule: %v", schedule)
		}
		if actualMax != 12 && actualMin != 11 {
			t.Errorf("desired min: 11, max: 12, actual min: %v, max: %v", actualMin, actualMax)
		}
	})
}

func TestNotInCronScheduleFirst(t *testing.T) {
	//testTime1, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-11-22 13:04:41", time.Local)
	//if err != nil {
	//	t.Fatal(err)
	//}
	t1 := time.Now()
	testTime1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 14, 05, 41, 0, t1.Location())
	lastTime := metav1.Time{Time: testTime1.Add(-1 * time.Second)}
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{
			LastCronScheduleTime: &lastTime,
		},
	}
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
					Schedule:    "0-4 10-12 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
				},
				{
					Schedule:    "0-59 9-10 * * *",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
				},
				{
					Schedule:    "0-4 13-14 * * *",
					MinReplicas: intPtr(11),
					MaxReplicas: 12,
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
		if schedule != "default" {
			t.Errorf("desired schedule: `default`, actual schedule: %v", schedule)
		}
		if actualMax != 10 && actualMin != 9 {
			t.Errorf("desired min: 9, max: 10, actual min: %v, max: %v", actualMin, actualMax)
		}
	})
}

func TestAcrossPeriods(t *testing.T) {
	//testTime1, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-11-22 12:58:59", time.Local)
	//if err != nil {
	//	t.Fatal(err)
	//}
	t1 := time.Now()
	testTime1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 12, 58, 59, 0, t1.Location())
	lastTime := metav1.Time{Time: testTime1.Add(-1 * time.Second)}
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{
			LastCronScheduleTime: &lastTime,
		},
	}
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
					Schedule:    "0-59 12 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
				},
				{
					Schedule:    "30-59 13-16 * * *",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
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
		if schedule != "0-59 12 * * *" {
			t.Errorf("desired schedule: `0-59 10-12 * * *`, actual schedule: %v", schedule)
		}
		if actualMax != 7 && actualMin != 5 {
			t.Errorf("desired min: 5, max: 7, actual min: %v, max: %v", actualMin, actualMax)
		}
	})
}

func TestAcrossPeriodsSecond(t *testing.T) {
	//testTime1, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-11-22 12:59:41", time.Local)
	//if err != nil {
	//	t.Fatal(err)
	//}
	t1 := time.Now()
	testTime1 := time.Date(t1.Year(), t1.Month(), t1.Day(), 12, 59, 41, 0, t1.Location())

	lastTime := metav1.Time{Time: testTime1.Add(-1 * time.Second)}
	gpa := &v1alpha1.GeneralPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.Time{Time: testTime1.Add(-60 * time.Minute)},
		},
		Status: v1alpha1.GeneralPodAutoscalerStatus{
			LastCronScheduleTime: &lastTime,
		},
	}
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
					Schedule:    "0-59 12 * * *",
					MinReplicas: intPtr(5),
					MaxReplicas: 7,
				},
				{
					Schedule:    "30-59 13-16 * * *",
					MinReplicas: intPtr(6),
					MaxReplicas: 8,
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
		if schedule != "0-59 12 * * *" {
			t.Errorf("desired schedule: `0-59 10-12 * * *`, actual schedule: %v", schedule)
		}
		if actualMax != 7 && actualMin != 5 {
			t.Errorf("desired min: 5, max: 7, actual min: %v, max: %v", actualMin, actualMax)
		}
	})
}
