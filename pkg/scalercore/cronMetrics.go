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
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron"
	"k8s.io/klog"

	"github.com/ocgi/general-pod-autoscaler/pkg/apis/autoscaling/v1alpha1"
)

var _ Scaler = &CronMetricsScaler{}
var recordCronMetricsScheduleName = ""

// CronMetricsScaler is a crontab GPA
type CronMetricsScaler struct {
	ranges     []v1alpha1.CronMetricSpec
	defaultSet v1alpha1.CronMetricSpec
	name       string
	now        time.Time
}

// NewCronMetricsScaler initializer crontab GPA
func NewCronMetricsScaler(ranges []v1alpha1.CronMetricSpec) *CronMetricsScaler {
	var def v1alpha1.CronMetricSpec
	filter := make([]v1alpha1.CronMetricSpec, 0)
	for _, cr := range ranges {
		if cr.Schedule != "default" {
			filter = append(filter, cr)
		} else {
			def = cr
		}
	}
	return &CronMetricsScaler{ranges: filter, name: Cron, now: time.Now(), defaultSet: def}
}

// GetReplicas return replicas  recommend by crontab GPA
func (s *CronMetricsScaler) GetReplicas(gpa *v1alpha1.GeneralPodAutoscaler, currentReplicas int32) (int32, error) {
	var max int32 = 0
	for _, t := range s.ranges {
		misMatch, finalMatch, err := s.getFinalMatchAndMisMatch(gpa, t.Schedule)
		if err != nil {
			klog.Error(err)
			return currentReplicas, nil
		}
		klog.Infof("firstMisMatch: %v, finalMatch: %v", misMatch, finalMatch)
		if finalMatch == nil {
			continue
		}
		if max < t.MaxReplicas {
			max = t.MaxReplicas
			recordCronMetricsScheduleName = t.Schedule
		}
		klog.Infof("Schedule %v recommend %v replicas, desire: %v", t.Schedule, max, t.MaxReplicas)
	}
	if max == 0 {
		klog.Info("Recommend 0 replicas, use current replicas number")
		max = gpa.Status.DesiredReplicas
	}
	return max, nil
}

// GetCurrentMaxAndMinReplicas get current cron config max and min replicas
func (s *CronMetricsScaler) GetCurrentMaxAndMinReplicas(gpa *v1alpha1.GeneralPodAutoscaler) (int32, int32, string) {
	var max, min int32
	if s.defaultSet.MaxReplicas == 0 && s.defaultSet.MinReplicas == nil {
		klog.Errorf("gpa %v not set default scheduler", gpa)
		return 2, 4, "default empty"
	}
	//use defaultSet max min replicas
	max = s.defaultSet.MaxReplicas
	min = *s.defaultSet.MinReplicas
	recordCronMetricsScheduleName = s.defaultSet.Schedule
	//only one schedule satisfy
	crs := make([]v1alpha1.CronMetricSpec, 0)
	for _, cr := range s.ranges {
		if cr.Schedule == "default" {
			//ignore `default` cron set
			continue
		}
		misMatch, finalMatch, err := s.getFinalMatchAndMisMatch(gpa, cr.Schedule)
		if err != nil {
			//can't get final, use default max min replicas, avoid use 0 0 replace
			klog.Error(err)
			return max, min, recordCronMetricsScheduleName
		}
		klog.Infof("firstMisMatch: %v, finalMatch: %v, schedule: %v", misMatch, finalMatch, cr.Schedule)
		if finalMatch == nil {
			continue
		} else {
			// exist multi cr with Priority
			crs = append(crs, cr)
			//max = cr.MaxReplicas
			//min = *cr.MinReplicas
			//recordCronMetricsScheduleName = cr.Schedule
			//klog.Infof("Schedule %v recommend %v max replicas, min replicas: %v", cr.Schedule, max, min)
			//return max, min, recordCronMetricsScheduleName
		}
	}
	klog.Infof("get crs: %v", crs)
	// not found, use default
	if len(crs) == 0 {
		return max, min, recordCronMetricsScheduleName
	}
	var maxPriority int
	var maxCr v1alpha1.CronMetricSpec
	// choose max priority cron spec
	for _, cr := range crs {
		// equal some old cronHpa config not set Priority
		if cr.Priority >= maxPriority {
			maxPriority = cr.Priority
			maxCr = cr
		}
	}
	max = maxCr.MaxReplicas
	min = *maxCr.MinReplicas
	recordCronMetricsScheduleName = maxCr.Schedule
	klog.Infof("Schedule %v recommend %v max replicas, min replicas: %v, Priority: %d",
		maxCr.Schedule, max, min, maxCr.Priority)
	return max, min, recordCronMetricsScheduleName
}

// GetCurrentCronMetricSpecs get schedule relate cronMetricSpec
func (s *CronMetricsScaler) GetCurrentCronMetricSpecs(gpa *v1alpha1.GeneralPodAutoscaler, schedule string) []v1alpha1.CronMetricSpec {
	cronMetricSpecs := gpa.Spec.CronMetricMode.CronMetrics
	expectedCronMetricSpecs := make([]v1alpha1.CronMetricSpec, 0)
	for _, cronInfo := range cronMetricSpecs {
		if cronInfo.Schedule == schedule {
			expectedCronMetricSpecs = append(expectedCronMetricSpecs, cronInfo)
		}
	}
	return expectedCronMetricSpecs
}

// ScalerName returns scaler name
func (s *CronMetricsScaler) ScalerName() string {
	return s.name
}

func (s *CronMetricsScaler) getFinalMatchAndMisMatch(gpa *v1alpha1.GeneralPodAutoscaler, schedule string) (*time.Time, *time.Time, error) {
	year, sched, err := ParseStandardWithYear(schedule)
	if err != nil {
		klog.Errorf("ParseStandardWithYear err: %s", err)
		return nil, nil, err
	}
	// year is not zero, not same with s.now then ignore
	// year is zero, not set year scheduled
	if year != 0 && year != s.now.Year() {
		return nil, nil, nil
	}
	//sched, err := cron.ParseStandard(schedule)
	//if err != nil {
	//	return nil, nil, err
	//}
	//lastTime := gpa.Status.LastCronScheduleTime.DeepCopy()
	//if recordCronMetricsScheduleName != schedule {
	//	lastTime = nil
	//}
	//if lastTime == nil || lastTime.IsZero() {
	//	lastTime = gpa.CreationTimestamp.DeepCopy()
	//}
	// fix bug: create time 12:08:31, now 12:09:01
	// schedule: 10-14 12 * * *
	initTime := getYesterdayFirstTime()
	match := initTime
	misMatch := initTime
	klog.Infof("Init time: %v, now: %v", initTime, s.now)
	t := initTime
	for {
		if !t.After(s.now) {
			misMatch = t
			t = sched.Next(t)
			continue
		}
		match = t
		break
	}
	klog.Infof("get misMatch: %s, match: %s", misMatch, match)
	// fix bug: misMatch diff s.now < 1 ,but match diff s.now > 1
	// fix bug: misMatch minute is 59, now is xx:59:02
	// fix bug: current time(now) is the hour and the second, 16:59:00.000, use equal check
	if s.now.Sub(misMatch).Minutes() <= 1 && (s.now.After(misMatch) || s.now.Equal(misMatch)) &&
		(match.Sub(s.now).Minutes() <= 1 || misMatch.Minute() == s.now.Minute()) {
		return &misMatch, &match, nil
	}

	return nil, nil, nil
}

// getYesterdayFirstTime get today init start time
func getYesterdayFirstTime() time.Time {
	t1 := time.Now().Add(-1 * time.Hour)
	return time.Date(t1.Year(), t1.Month(), t1.Day(), t1.Hour(), 0, 0, 0, t1.Location())
}

// ParseStandardWithYear parse schedule with year
func ParseStandardWithYear(schedule string) (int, cron.Schedule, error) {
	schSlice := strings.Split(schedule, " ")
	if len(schSlice) > 5 {
		year, err := strconv.Atoi(schSlice[len(schSlice)-1])
		if err != nil {
			return 0, nil, err
		}
		leaveSchedule := strings.Join(schSlice[:len(schSlice)-1], " ")
		klog.Infof("get year: %s, schedule: %s, leave schedule: %s", schSlice[len(schSlice)-1],
			schedule, leaveSchedule)
		sched, err := cron.ParseStandard(leaveSchedule)
		return year, sched, err
	}
	sched, err := cron.ParseStandard(schedule)
	return 0, sched, err
}
