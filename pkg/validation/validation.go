// Copyright 2021 The OCGI Authors.
// Copyright 2021 The Kubernetes Authors.
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
	"fmt"
	"time"

	"github.com/ocgi/general-pod-autoscaler/pkg/scalercore"

	"k8s.io/klog"

	"github.com/robfig/cron"
	"k8s.io/api/admissionregistration/v1beta1"
	apimachineryvalidation "k8s.io/apimachinery/pkg/api/validation"
	pathvalidation "k8s.io/apimachinery/pkg/api/validation/path"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/util/webhook"

	mapset "github.com/deckarep/golang-set"
	autoscaling "github.com/ocgi/general-pod-autoscaler/pkg/apis/autoscaling/v1alpha1"
)

const (
	// MaxPeriodSeconds is the largest allowed scaling policy period (in seconds)
	MaxPeriodSeconds int32 = 1800
	// MaxStabilizationWindowSeconds is the largest allowed stabilization window (in seconds)
	MaxStabilizationWindowSeconds int32 = 3600
)

// ValidateHorizontalPodAutoscalerName can be used to check whether the given autoscaler name is valid.
// Prefix indicates this name will be used as part of generation, in which case trailing dashes are allowed.
var ValidateHorizontalPodAutoscalerName = apimachineryvalidation.NameIsDNSSubdomain

func validateHorizontalPodAutoscalerSpec(autoscaler autoscaling.GeneralPodAutoscalerSpec, fldPath *field.Path,
	minReplicasLowerBound int32) field.ErrorList {
	allErrs := field.ErrorList{}

	if autoscaler.AutoScalingDrivenMode.CronMetricMode != nil {
		klog.Infof("Run cronHpa validate")
		if refErrs := validateCronMetric(autoscaler.AutoScalingDrivenMode.CronMetricMode, fldPath.Child("cronMetric"), minReplicasLowerBound); len(refErrs) > 0 {
			allErrs = append(allErrs, refErrs...)
		}
	} else {
		if autoscaler.MinReplicas != nil && *autoscaler.MinReplicas < minReplicasLowerBound {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("minReplicas"), *autoscaler.MinReplicas,
				fmt.Sprintf("must be greater than or equal to %d", minReplicasLowerBound)))
		}
		if autoscaler.MaxReplicas < 1 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("maxReplicas"), autoscaler.MaxReplicas, "must be greater than 0"))
		}
		if autoscaler.MinReplicas != nil && autoscaler.MaxReplicas < *autoscaler.MinReplicas {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("maxReplicas"), autoscaler.MaxReplicas, "must be greater than or equal to `minReplicas`"))
		}
	}
	if refErrs := ValidateCrossVersionObjectReference(autoscaler.ScaleTargetRef, fldPath.Child("scaleTargetRef")); len(refErrs) > 0 {
		allErrs = append(allErrs, refErrs...)
	}
	if autoscaler.AutoScalingDrivenMode.MetricMode != nil {
		if refErrs := validateMetrics(autoscaler.AutoScalingDrivenMode.MetricMode.Metrics, fldPath.Child("metrics"), autoscaler.MinReplicas); len(refErrs) > 0 {
			allErrs = append(allErrs, refErrs...)
		}
	}
	if autoscaler.AutoScalingDrivenMode.WebhookMode != nil {
		if refErrs := validateWebhook(autoscaler.AutoScalingDrivenMode.WebhookMode.WebhookClientConfig, fldPath.Child("webhook")); len(refErrs) > 0 {
			allErrs = append(allErrs, refErrs...)
		}
	}
	if autoscaler.AutoScalingDrivenMode.TimeMode != nil {
		if refErrs := validateTime(autoscaler.AutoScalingDrivenMode.TimeMode.TimeRanges, fldPath.Child("time")); len(refErrs) > 0 {
			allErrs = append(allErrs, refErrs...)
		}
	}
	if autoscaler.AutoScalingDrivenMode.EventMode != nil {
		if refErrs := validateEvent(autoscaler.AutoScalingDrivenMode.EventMode.Triggers, fldPath.Child("event")); len(refErrs) > 0 {
			allErrs = append(allErrs, refErrs...)
		}
	}
	if refErrs := validateBehavior(autoscaler.Behavior, fldPath.Child("behavior")); len(refErrs) > 0 {
		allErrs = append(allErrs, refErrs...)
	}
	return allErrs
}

// ValidateCrossVersionObjectReference validates a CrossVersionObjectReference and returns an
// ErrorList with any errors.
func ValidateCrossVersionObjectReference(ref autoscaling.CrossVersionObjectReference, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if len(ref.Kind) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("kind"), ""))
	} else {
		for _, msg := range pathvalidation.IsValidPathSegmentName(ref.Kind) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("kind"), ref.Kind, msg))
		}
	}

	if len(ref.Name) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("name"), ""))
	} else {
		for _, msg := range pathvalidation.IsValidPathSegmentName(ref.Name) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("name"), ref.Name, msg))
		}
	}

	return allErrs
}

// ValidateHorizontalPodAutoscaler validates a HorizontalPodAutoscaler and returns an
// ErrorList with any errors.
func ValidateHorizontalPodAutoscaler(autoscaler *autoscaling.GeneralPodAutoscaler) field.ErrorList {
	allErrs := apimachineryvalidation.ValidateObjectMeta(&autoscaler.ObjectMeta, true, ValidateHorizontalPodAutoscalerName,
		field.NewPath("metadata"))

	// MinReplicasLowerBound represents a minimum value for minReplicas
	// 0 when GPA scale-to-zero feature is enabled
	var minReplicasLowerBound int32

	allErrs = append(allErrs, validateHorizontalPodAutoscalerSpec(autoscaler.Spec, field.NewPath("spec"), minReplicasLowerBound)...)
	return allErrs
}

// ValidateHorizontalPodAutoscalerUpdate validates an update to a HorizontalPodAutoscaler and returns an
// ErrorList with any errors.
func ValidateHorizontalPodAutoscalerUpdate(newAutoscaler, oldAutoscaler *autoscaling.GeneralPodAutoscaler) field.ErrorList {
	allErrs := apimachineryvalidation.ValidateObjectMetaUpdate(&newAutoscaler.ObjectMeta, &oldAutoscaler.ObjectMeta, field.NewPath("metadata"))

	// minReplicasLowerBound represents a minimum value for minReplicas
	// 0 when GPA scale-to-zero feature is enabled or GPA object already has minReplicas=0
	var minReplicasLowerBound int32
	allErrs = append(allErrs, validateHorizontalPodAutoscalerSpec(newAutoscaler.Spec, field.NewPath("spec"), minReplicasLowerBound)...)
	return allErrs
}

// ValidateHorizontalPodAutoscalerStatusUpdate validates an update to status on a HorizontalPodAutoscaler and
// returns an ErrorList with any errors.
func ValidateHorizontalPodAutoscalerStatusUpdate(newAutoscaler, oldAutoscaler *autoscaling.GeneralPodAutoscaler) field.ErrorList {
	allErrs := apimachineryvalidation.ValidateObjectMetaUpdate(&newAutoscaler.ObjectMeta, &oldAutoscaler.ObjectMeta, field.NewPath("metadata"))
	status := newAutoscaler.Status
	allErrs = append(allErrs, apimachineryvalidation.ValidateNonnegativeField(int64(status.CurrentReplicas), field.NewPath("status", "currentReplicas"))...)
	allErrs = append(allErrs, apimachineryvalidation.ValidateNonnegativeField(int64(status.DesiredReplicas), field.NewPath("status", "desiredReplicas"))...)
	return allErrs
}

// CronSet set to check conflict
type CronSet struct {
	schedule string
	Type     string
	Priority int
	set      mapset.Set
}

func validateCronMetric(cronMetricMode *autoscaling.CronMetricMode, fldPath *field.Path, minReplicasLowerBound int32) field.ErrorList {
	allErrs := field.ErrorList{}
	if len(cronMetricMode.CronMetrics) == 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("cronMetrics"), "at least one cronMetrics should set"))
	}

	var defaultSetNum int
	start := time.Now()
	cycleSetSlice := make([]CronSet, 0)
	customSetSlice := make([]CronSet, 0)
	defaultCronSpec := make([]autoscaling.CronMetricSpec, 0)
	klog.Infof("webhook cronMetrics: %v", cronMetricMode.CronMetrics)
	for _, cronRange := range cronMetricMode.CronMetrics {
		if cronRange.MinReplicas != nil && *cronRange.MinReplicas < minReplicasLowerBound {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("minReplicas"), *cronRange.MinReplicas,
				fmt.Sprintf("must be greater than or equal to %d", minReplicasLowerBound)))
		}
		if cronRange.MaxReplicas < 1 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("maxReplicas"), cronRange.MaxReplicas, "must be greater than 0"))
		}
		if cronRange.MinReplicas != nil && cronRange.MaxReplicas < *cronRange.MinReplicas {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("maxReplicas"), cronRange.MaxReplicas, "must be greater than or equal to `minReplicas`"))
		}
		if len(cronRange.Schedule) == 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("schedule"), "should not empty"))
		} else {
			if cronRange.Schedule == "default" {
				//default cron set, ignore conflict check
				defaultSetNum += 1
				defaultCronSpec = append(defaultCronSpec, cronRange)
				continue
			}
			year, sch, err := scalercore.ParseStandardWithYear(cronRange.Schedule)
			//sch, err := cron.ParseStandard(cronRange.Schedule)
			if err != nil {
				allErrs = append(allErrs, field.Forbidden(fldPath.Child("schedule"), err.Error()))
				continue
			}
			schSet := mapset.NewSet()
			// year is not zero add to cycleSetSlice to validate
			if year != 0 {
				// must set year to start, example: now is 2023, but set 2024 cron set
				next := time.Date(year, start.Month(), start.Day(), start.Hour(), start.Minute(),
					start.Second(), start.Nanosecond(), start.Location())
				for {
					next = sch.Next(next)
					schSet.Add(next)
					if next.Year() != year {
						break
					}
				}
				cycleSetSlice = append(cycleSetSlice, CronSet{
					cronRange.Schedule,
					string(cronRange.ContainerResource.Name),
					cronRange.Priority,
					schSet,
				})
			} else {
				next := start
				for {
					next = sch.Next(next)
					schSet.Add(next)
					if next.Month() != start.Month() {
						break
					}
				}
				newSchSet := mapset.NewSet()
				for _, date := range schSet.ToSlice() {
					dataTime := date.(time.Time)
					newDataTime := time.Date(year, dataTime.Month(), dataTime.Day(), dataTime.Hour(), dataTime.Minute(),
						dataTime.Second(), dataTime.Nanosecond(), dataTime.Location())
					newSchSet.Add(newDataTime)
				}
				customSetSlice = append(customSetSlice, CronSet{
					cronRange.Schedule,
					string(cronRange.ContainerResource.Name),
					cronRange.Priority,
					newSchSet,
				})
			}
		}
	}
	// allow set two default, but min and max need same
	// not set default is forbidden
	if defaultSetNum <= 0 || defaultSetNum > 2 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("cronMetrics"), "only two or one `default` schedule cronMetrics should set"))
	}
	if defaultSetNum == 2 {
		first := defaultCronSpec[0]
		two := defaultCronSpec[1]
		klog.Infof("first: %v, two: %v", first, two)
		if first.MaxReplicas != two.MaxReplicas || *first.MinReplicas != *two.MinReplicas {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("cronMetrics"), "two `default` schedule"+
				" cronMetrics must with same minReplicates and maxReplicates set"))
		}
	}
	allErrs = checkConflict(cycleSetSlice, allErrs, fldPath)
	allErrs = checkConflict(customSetSlice, allErrs, fldPath)
	return allErrs
}

// checkConflict check CronSet conflict info
func checkConflict(setSlice []CronSet, allErrs field.ErrorList, fldPath *field.Path) field.ErrorList {
	for i := 0; i <= len(setSlice); i++ {
		for j := i + 1; j < len(setSlice); j++ {
			if setSlice[i].Type != setSlice[j].Type && setSlice[i].schedule == setSlice[j].schedule {
				// ignore cpu and mem set with same schedule
				continue
			}
			IntersectSet := setSlice[i].set.Intersect(setSlice[j].set)
			// Priority all true, but Cardinality time
			if IntersectSet.Cardinality() > 0 && (setSlice[i].Priority == setSlice[j].Priority) {
				allErrs = append(allErrs, field.Forbidden(fldPath.Child("schedule"),
					fmt.Sprintf("schedule time conflict, schedule: %s conflict with %s, Priority: %v, Priority: %v", setSlice[i].schedule,
						setSlice[j].schedule, setSlice[i].Priority, setSlice[j].Priority)))
				break
			}
		}
	}
	return allErrs
}

func validateMetrics(metrics []autoscaling.MetricSpec, fldPath *field.Path, minReplicas *int32) field.ErrorList {
	allErrs := field.ErrorList{}
	hasObjectMetrics := false
	hasExternalMetrics := false

	for i, metricSpec := range metrics {
		idxPath := fldPath.Index(i)
		if targetErrs := validateMetricSpec(metricSpec, idxPath); len(targetErrs) > 0 {
			allErrs = append(allErrs, targetErrs...)
		}
		if metricSpec.Type == autoscaling.ObjectMetricSourceType {
			hasObjectMetrics = true
		}
		if metricSpec.Type == autoscaling.ExternalMetricSourceType {
			hasExternalMetrics = true
		}
	}

	if minReplicas != nil && *minReplicas == 0 {
		if !hasObjectMetrics && !hasExternalMetrics {
			allErrs = append(allErrs, field.Forbidden(fldPath, "must specify at least one Object or External metric to support scaling to zero replicas"))
		}
	}

	return allErrs
}

func validateWebhook(wc *v1beta1.WebhookClientConfig, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if wc == nil {
		allErrs = append(allErrs, field.Forbidden(fldPath, "webhook config should not be empty"))
		return allErrs
	}
	switch {
	case wc.Service == nil && wc.URL == nil:
		allErrs = append(allErrs, field.Forbidden(fldPath, "must specify at least one service or url"))

	case wc.URL != nil:
		allErrs = append(allErrs, webhook.ValidateWebhookURL(fldPath.Child("webhook").Child("url"), *wc.URL, false)...)
	case wc.Service != nil:
		var port int32 = 0
		if wc.Service.Port != nil {
			port = *wc.Service.Port
		}
		allErrs = append(allErrs, webhook.ValidateWebhookService(fldPath.Child("webhook").Child("service"), wc.Service.Name, wc.Service.Namespace,
			wc.Service.Path, port)...)
	}
	return allErrs
}

func validateTime(timeRanges []autoscaling.TimeRange, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if len(timeRanges) == 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("timeRanges"), "at least one timeRanges should set"))
	}
	for _, timeRange := range timeRanges {
		if timeRange.DesiredReplicas == 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("desiredReplicas"), "should not 0"))
		}
		if len(timeRange.Schedule) == 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("schedule"), "should not empty"))
		} else {
			_, err := cron.Parse(timeRange.Schedule)
			if err != nil {
				allErrs = append(allErrs, field.Forbidden(fldPath.Child("schedule"), err.Error()))
			}
		}
	}
	return allErrs
}

func validateEvent(triggers []autoscaling.ScaleTriggers, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if len(triggers) == 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("triggers"), "at least one trigger should set"))
	}
	for _, trigger := range triggers {
		if len(trigger.Type) == 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("type"), "trigger type must set"))

		}
		if len(trigger.Metadata) == 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("medadata"), "trigger medadata must set"))
		}
	}
	return allErrs
}

func validateBehavior(behavior *autoscaling.GeneralPodAutoscalerBehavior, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if behavior != nil {
		if scaleUpErrs := validateScalingRules(behavior.ScaleUp, fldPath.Child("scaleUp")); len(scaleUpErrs) > 0 {
			allErrs = append(allErrs, scaleUpErrs...)
		}
		if scaleDownErrs := validateScalingRules(behavior.ScaleDown, fldPath.Child("scaleDown")); len(scaleDownErrs) > 0 {
			allErrs = append(allErrs, scaleDownErrs...)
		}
	}
	return allErrs
}

var validSelectPolicyTypes = sets.NewString(string(autoscaling.MaxPolicySelect), string(autoscaling.MinPolicySelect), string(autoscaling.DisabledPolicySelect))
var validSelectPolicyTypesList = validSelectPolicyTypes.List()

func validateScalingRules(rules *autoscaling.GPAScalingRules, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if rules != nil {
		if rules.StabilizationWindowSeconds != nil && *rules.StabilizationWindowSeconds < 0 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("stabilizationWindowSeconds"), rules.StabilizationWindowSeconds, "must be greater than or equal to zero"))
		}
		if rules.StabilizationWindowSeconds != nil && *rules.StabilizationWindowSeconds > MaxStabilizationWindowSeconds {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("stabilizationWindowSeconds"), rules.StabilizationWindowSeconds,
				fmt.Sprintf("must be less than or equal to %v", MaxStabilizationWindowSeconds)))
		}
		if rules.SelectPolicy != nil && !validSelectPolicyTypes.Has(string(*rules.SelectPolicy)) {
			allErrs = append(allErrs, field.NotSupported(fldPath.Child("selectPolicy"), rules.SelectPolicy, validSelectPolicyTypesList))
		}
		policiesPath := fldPath.Child("policies")
		if len(rules.Policies) == 0 {
			allErrs = append(allErrs, field.Required(policiesPath, "must specify at least one Policy"))
		}
		for i, policy := range rules.Policies {
			idxPath := policiesPath.Index(i)
			if policyErrs := validateScalingPolicy(policy, idxPath); len(policyErrs) > 0 {
				allErrs = append(allErrs, policyErrs...)
			}
		}
	}
	return allErrs
}

var validPolicyTypes = sets.NewString(string(autoscaling.PodsScalingPolicy), string(autoscaling.PercentScalingPolicy))
var validPolicyTypesList = validPolicyTypes.List()

func validateScalingPolicy(policy autoscaling.GPAScalingPolicy, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if policy.Type != autoscaling.PodsScalingPolicy && policy.Type != autoscaling.PercentScalingPolicy {
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("type"), policy.Type, validPolicyTypesList))
	}
	if policy.Value <= 0 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("value"), policy.Value, "must be greater than zero"))
	}
	if policy.PeriodSeconds <= 0 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("periodSeconds"), policy.PeriodSeconds, "must be greater than zero"))
	}
	if policy.PeriodSeconds > MaxPeriodSeconds {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("periodSeconds"), policy.PeriodSeconds,
			fmt.Sprintf("must be less than or equal to %v", MaxPeriodSeconds)))
	}
	return allErrs
}

var validMetricSourceTypes = sets.NewString(
	string(autoscaling.ObjectMetricSourceType),
	string(autoscaling.PodsMetricSourceType),
	string(autoscaling.ResourceMetricSourceType),
	string(autoscaling.ContainerResourceMetricSourceType),
	string(autoscaling.ExternalMetricSourceType))
var validMetricSourceTypesList = validMetricSourceTypes.List()

func validateMetricSpec(spec autoscaling.MetricSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(string(spec.Type)) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("type"), "must specify a metric source type"))
	}

	if !validMetricSourceTypes.Has(string(spec.Type)) {
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("type"), spec.Type, validMetricSourceTypesList))
	}

	typesPresent := sets.NewString()
	if spec.Object != nil {
		typesPresent.Insert("object")
		if typesPresent.Len() == 1 {
			allErrs = append(allErrs, validateObjectSource(spec.Object, fldPath.Child("object"))...)
		}
	}

	if spec.External != nil {
		typesPresent.Insert("external")
		if typesPresent.Len() == 1 {
			allErrs = append(allErrs, validateExternalSource(spec.External, fldPath.Child("external"))...)
		}
	}

	if spec.Pods != nil {
		typesPresent.Insert("pods")
		if typesPresent.Len() == 1 {
			allErrs = append(allErrs, validatePodsSource(spec.Pods, fldPath.Child("pods"))...)
		}
	}

	if spec.Resource != nil {
		typesPresent.Insert("resource")
		if typesPresent.Len() == 1 {
			allErrs = append(allErrs, validateResourceSource(spec.Resource, fldPath.Child("resource"))...)
		}
	}

	var expectedField string
	switch spec.Type {

	case autoscaling.ObjectMetricSourceType:
		if spec.Object == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("object"), "must populate information for the given metric source"))
		}
		expectedField = "object"
	case autoscaling.PodsMetricSourceType:
		if spec.Pods == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("pods"), "must populate information for the given metric source"))
		}
		expectedField = "pods"
	case autoscaling.ResourceMetricSourceType:
		if spec.Resource == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("resource"), "must populate information for the given metric source"))
		}
		expectedField = "resource"
	case autoscaling.ContainerResourceMetricSourceType:
		if spec.ContainerResource == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("containerResource"), "must populate information for the given metric source"))
		}
		expectedField = "containerResource"
	case autoscaling.ExternalMetricSourceType:
		if spec.External == nil {
			allErrs = append(allErrs, field.Required(fldPath.Child("external"), "must populate information for the given metric source"))
		}
		expectedField = "external"
	default:
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("type"), spec.Type, validMetricSourceTypesList))
	}

	if typesPresent.Len() != 1 {
		typesPresent.Delete(expectedField)
		for typ := range typesPresent {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child(typ), "must populate the given metric source only"))
		}
	}

	return allErrs
}

func validateObjectSource(src *autoscaling.ObjectMetricSource, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateCrossVersionObjectReference(src.DescribedObject, fldPath.Child("describedObject"))...)
	allErrs = append(allErrs, validateMetricIdentifier(src.Metric, fldPath.Child("metric"))...)
	allErrs = append(allErrs, validateMetricTarget(src.Target, fldPath.Child("target"))...)

	if src.Target.Value == nil && src.Target.AverageValue == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("target").Child("averageValue"), "must set either a target value or averageValue"))
	}

	return allErrs
}

func validateExternalSource(src *autoscaling.ExternalMetricSource, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateMetricIdentifier(src.Metric, fldPath.Child("metric"))...)
	allErrs = append(allErrs, validateMetricTarget(src.Target, fldPath.Child("target"))...)

	if src.Target.Value == nil && src.Target.AverageValue == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("target").Child("averageValue"), "must set either a target value for metric or a per-pod target"))
	}

	if src.Target.Value != nil && src.Target.AverageValue != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("target").Child("value"), "may not set both a target value for metric and a per-pod target"))
	}

	return allErrs
}

func validatePodsSource(src *autoscaling.PodsMetricSource, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateMetricIdentifier(src.Metric, fldPath.Child("metric"))...)
	allErrs = append(allErrs, validateMetricTarget(src.Target, fldPath.Child("target"))...)

	if src.Target.AverageValue == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("target").Child("averageValue"), "must specify a positive target averageValue"))
	}

	return allErrs
}

func validateResourceSource(src *autoscaling.ResourceMetricSource, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(src.Name) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("name"), "must specify a resource name"))
	}

	allErrs = append(allErrs, validateMetricTarget(src.Target, fldPath.Child("target"))...)

	if src.Target.AverageUtilization == nil && src.Target.AverageValue == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("target").Child("averageUtilization"), "must set either a target raw value or a target utilization"))
	}

	if src.Target.AverageUtilization != nil && src.Target.AverageValue != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("target").Child("averageValue"), "may not set both a target raw value and a target utilization"))
	}

	return allErrs
}

func validateMetricTarget(mt autoscaling.MetricTarget, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(mt.Type) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("type"), "must specify a metric target type"))
	}

	if mt.Type != autoscaling.UtilizationMetricType &&
		mt.Type != autoscaling.ValueMetricType &&
		mt.Type != autoscaling.AverageValueMetricType {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("type"), mt.Type, "must be either Utilization, Value, or AverageValue"))
	}

	if mt.AverageUtilization == nil && mt.AverageValue == nil && mt.Value == nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Child(" utilization, value and averageValue"), mt.Type, "at least one not nil"))
	}

	if mt.Value != nil && mt.Value.Sign() != 1 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("value"), mt.Value, "must be positive"))
	}

	if mt.AverageValue != nil && mt.AverageValue.Sign() != 1 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("averageValue"), mt.AverageValue, "must be positive"))
	}

	if mt.AverageUtilization != nil && *mt.AverageUtilization < 1 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("averageUtilization"), mt.AverageUtilization, "must be greater than 0"))
	}

	return allErrs
}

func validateMetricIdentifier(id autoscaling.MetricIdentifier, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(id.Name) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("name"), "must specify a metric name"))
	} else {
		for _, msg := range pathvalidation.IsValidPathSegmentName(id.Name) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("name"), id.Name, msg))
		}
	}
	return allErrs
}
