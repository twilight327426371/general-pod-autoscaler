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

package scaler

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"

	pkgerrors "github.com/pkg/errors"
	autoscalinginternal "k8s.io/api/autoscaling/v1"
	v1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	scaleclient "k8s.io/client-go/scale"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	autoscaling "github.com/ocgi/general-pod-autoscaler/pkg/apis/autoscaling/v1alpha1"
	autoscalingclient "github.com/ocgi/general-pod-autoscaler/pkg/client/clientset/versioned/typed/autoscaling/v1alpha1"
	autoscalinginformers "github.com/ocgi/general-pod-autoscaler/pkg/client/informers/externalversions/autoscaling/v1alpha1"
	autoscalinglisters "github.com/ocgi/general-pod-autoscaler/pkg/client/listers/autoscaling/v1alpha1"
	metricsclient "github.com/ocgi/general-pod-autoscaler/pkg/metrics"
	"github.com/ocgi/general-pod-autoscaler/pkg/scalercore"
	"github.com/ocgi/general-pod-autoscaler/pkg/util"
)

var (
	scaleUpLimitFactor  = 2.0
	scaleUpLimitMinimum = 4.0
	computeByLimitsKey  = "compute-by-limits"
)

type ScaleEvent struct {
	OldReplicas          int32   `json:"old_replicas"`
	NewReplicas          int32   `json:"new_replicas"`
	MinReplicas          int32   `json:"min_replicas"`
	MaxReplicas          int32   `json:"max_replicas"`
	CpuRequestsOfChanges float32 `json:"cpu_request_of_changes"` //increment/decrement of cpu requests
	CpuLimitsOfChanges   float32 `json:"cpu_limits_of_changes"`  //increment/decrement of cpu requests
	MemRequestsOfChanges float32 `json:"mem_request_of_changes"` //increment/decrement of cpu requests
	MemLimitsOfChanges   float32 `json:"mem_limits_of_changes"`  //increment/decrement of cpu requests
	Reason               string  `json:"reason"`
}

type timestampedRecommendation struct {
	recommendation int32
	timestamp      time.Time
}

type timestampedScaleEvent struct {
	replicaChange int32 // positive for scaleUp, negative for scaleDown
	timestamp     time.Time
	outdated      bool
}

// GeneralController is responsible for the synchronizing GPA objects stored
// in the system with the actual deployments/replication controllers they
// control.
type GeneralController struct {
	scaleNamespacer scaleclient.ScalesGetter
	gpaNamespacer   autoscalingclient.GeneralPodAutoscalersGetter
	mapper          apimeta.RESTMapper

	replicaCalc   *ReplicaCalculator
	eventRecorder record.EventRecorder

	downscaleStabilisationWindow time.Duration

	// gpaLister is able to list/get GPAs from the shared cache from the informer passed in to
	// NewGeneralController.
	gpaLister       autoscalinglisters.GeneralPodAutoscalerLister
	gpaListerSynced cache.InformerSynced

	// podLister is able to list/get Pods from the shared cache from the informer passed in to
	// NewGeneralController.
	podLister       corelisters.PodLister
	podListerSynced cache.InformerSynced

	// Controllers that need to be synced
	queue workqueue.RateLimitingInterface

	// Latest unstabilized recommendations for each autoscaler.
	recommendations map[string][]timestampedRecommendation

	// Multi goroutine read and write recommendations may unsafe.
	recommendationsLock sync.Mutex

	// Latest autoscaler events
	scaleUpEvents   map[string][]timestampedScaleEvent
	scaleDownEvents map[string][]timestampedScaleEvent

	// Multi goroutine read and write events may unsafe.
	scaleUpEventsLock   sync.Mutex
	scaleDownEventsLock sync.Mutex

	doingCron sync.Map

	workers int
}

// NewGeneralController creates a new GeneralController.
func NewGeneralController(
	evtNamespacer v1core.EventsGetter,
	scaleNamespacer scaleclient.ScalesGetter,
	gpaNamespacer autoscalingclient.GeneralPodAutoscalersGetter,
	mapper apimeta.RESTMapper,
	metricsClient metricsclient.MetricsClient,
	gpaInformer autoscalinginformers.GeneralPodAutoscalerInformer,
	podInformer coreinformers.PodInformer,
	resyncPeriod time.Duration,
	downscaleStabilisationWindow time.Duration,
	tolerance float64,
	cpuInitializationPeriod,
	delayOfInitialReadinessStatus time.Duration,
	workers int,
) *GeneralController {
	s := scheme.Scheme
	s.AddKnownTypes(autoscaling.SchemeGroupVersion, &autoscaling.GeneralPodAutoscaler{})
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(klog.Infof)
	broadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: evtNamespacer.Events(v1.NamespaceAll)})
	recorder := broadcaster.NewRecorder(s, v1.EventSource{Component: "pod-autoscaler"})

	gpaController := &GeneralController{
		eventRecorder:                recorder,
		scaleNamespacer:              scaleNamespacer,
		gpaNamespacer:                gpaNamespacer,
		downscaleStabilisationWindow: downscaleStabilisationWindow,
		queue: workqueue.NewNamedRateLimitingQueue(
			NewDefaultGPARateLimiter(resyncPeriod), "podautoscaler"),
		mapper:          mapper,
		recommendations: map[string][]timestampedRecommendation{},
		scaleUpEvents:   map[string][]timestampedScaleEvent{},
		scaleDownEvents: map[string][]timestampedScaleEvent{},
		workers:         workers,
	}

	gpaInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc:    gpaController.enqueueGPA,
			UpdateFunc: gpaController.updateGPA,
			DeleteFunc: gpaController.deleteGPA,
		},
		resyncPeriod,
	)
	gpaController.gpaLister = gpaInformer.Lister()
	gpaController.gpaListerSynced = gpaInformer.Informer().HasSynced

	gpaController.podLister = podInformer.Lister()
	gpaController.podListerSynced = podInformer.Informer().HasSynced

	replicaCalc := NewReplicaCalculator(
		metricsClient,
		gpaController.podLister,
		tolerance,
		cpuInitializationPeriod,
		delayOfInitialReadinessStatus,
	)
	gpaController.replicaCalc = replicaCalc

	return gpaController
}

// Run begins watching and syncing.
func (a *GeneralController) Run(stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer a.queue.ShutDown()

	klog.Infof("Starting GPA controller, workers is %v", a.workers)
	defer klog.Infof("Shutting down GPA controller")

	if !cache.WaitForNamedCacheSync("GPA", stopCh, a.gpaListerSynced, a.podListerSynced) {
		return
	}
	// start some workers
	for i := 0; i < a.workers; i++ {
		go wait.Until(a.worker, time.Second, stopCh)
	}

	<-stopCh
}

// obj could be an *v1.GeneralPodAutoscaler, or a DeletionFinalStateUnknown marker item.
func (a *GeneralController) updateGPA(old, cur interface{}) {
	a.enqueueGPA(cur)
}

// obj could be an *v1.GeneralPodAutoscaler, or a DeletionFinalStateUnknown marker item.
func (a *GeneralController) enqueueGPA(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	// Requests are always added to queue with resyncPeriod delay.  If there's already
	// request for the GPA in the queue then a new request is always dropped. Requests spend resync
	// interval in queue so GPAs are processed every resync interval.
	a.queue.AddRateLimited(key)
}

func (a *GeneralController) deleteGPA(obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for object %+v: %v", obj, err))
		return
	}

	// TODO: could we leak if we fail to get the key?
	a.queue.Forget(key)
}

func (a *GeneralController) worker() {
	for a.processNextWorkItem() {
	}
	klog.Infof("general pod autoscaler controller worker shutting down")
}

func (a *GeneralController) processNextWorkItem() bool {
	key, quit := a.queue.Get()
	if quit {
		return false
	}
	defer a.queue.Done(key)

	deleted, err := a.reconcileKey(key.(string))
	if err != nil {
		utilruntime.HandleError(err)
	}
	// Add request processing GPA to queue with resyncPeriod delay.
	// Requests are always added to queue with resyncPeriod delay. If there's already request
	// for the GPA in the queue then a new request is always dropped. Requests spend resyncPeriod
	// in queue so GPAs are processed every resyncPeriod.
	// Request is added here just in case last resync didn't insert request into the queue. This
	// happens quite often because there is race condition between adding request after resyncPeriod
	// and removing them from queue. Request can be added by resync before previous request is
	// removed from queue. If we didn't add request here then in this case one request would be dropped
	// and GPA would processed after 2 x resyncPeriod.
	if !deleted {
		a.queue.AddRateLimited(key)
	}
	return true
}

// computeReplicasForMetrics computes the desired number of replicas for the metric specifications listed in the GPA,
// returning the maximum  of the computed replica counts, a description of the associated metric, and the statuses of
// all metrics computed.
func (a *GeneralController) computeReplicasForMetrics(gpa *autoscaling.GeneralPodAutoscaler,
	scale *autoscalinginternal.Scale, metricSpecs []autoscaling.MetricSpec) (replicas int32, metric string,
	statuses []autoscaling.MetricStatus, timestamp time.Time, err error) {

	if scale.Status.Selector == "" {
		errMsg := "selector is required"
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "SelectorRequired", errMsg)
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "InvalidSelector",
			"the GPA target's scale is missing a selector")
		return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
	}

	selector, err := labels.Parse(scale.Status.Selector)
	if err != nil {
		errMsg := fmt.Sprintf("couldn't convert selector into a corresponding internal selector object: %v", err)
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "InvalidSelector", errMsg)
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "InvalidSelector", errMsg)
		return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
	}

	specReplicas := scale.Spec.Replicas
	statusReplicas := scale.Status.Replicas
	statuses = make([]autoscaling.MetricStatus, len(metricSpecs))

	invalidMetricsCount := 0
	var invalidMetricError error
	var invalidMetricCondition autoscaling.GeneralPodAutoscalerCondition

	for i, metricSpec := range metricSpecs {
		replicaCountProposal, metricNameProposal, timestampProposal, condition, err := a.computeReplicasForMetric(gpa,
			metricSpec, specReplicas, statusReplicas, selector, &statuses[i])
		if err != nil {
			if invalidMetricsCount <= 0 {
				invalidMetricCondition = condition
				invalidMetricError = err
			}
			invalidMetricsCount++
		}
		if err == nil && (replicas == 0 || replicaCountProposal > replicas) {
			timestamp = timestampProposal
			replicas = replicaCountProposal
			metric = metricNameProposal
		}
	}

	// If all metrics are invalid return error and set condition on gpa based on first invalid metric.
	if invalidMetricsCount >= len(metricSpecs) {
		setCondition(gpa, invalidMetricCondition.Type, invalidMetricCondition.Status, invalidMetricCondition.Reason,
			invalidMetricCondition.Message)
		return 0, "", statuses, time.Time{}, fmt.Errorf("invalid metrics (%v invalid out of %v), "+
			"first error is: %v", invalidMetricsCount, len(metricSpecs), invalidMetricError)
	}
	setCondition(gpa, autoscaling.ScalingActive, v1.ConditionTrue, "ValidMetricFound",
		"the GPA was able to successfully calculate a replica count from %s", metric)
	return replicas, metric, statuses, timestamp, nil
}

// computeReplicasForCronMetrics computes the desired number of replicas for the metric specifications listed in the GPA,
// returning the maximum  of the computed replica counts, a description of the associated metric, and the statuses of
// all metrics computed.
func (a *GeneralController) computeReplicasForCronMetrics(gpa *autoscaling.GeneralPodAutoscaler, scale *autoscalinginternal.Scale,
	metricSpecs []autoscaling.CronMetricSpec, scheduleName string) (replicas int32, metric string, statuses []autoscaling.MetricStatus, timestamp time.Time, err error) {
	if scale.Status.Selector == "" {
		errMsg := "selector is required"
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "SelectorRequired", errMsg)
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "InvalidSelector",
			"the GPA target's scale is missing a selector")
		return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
	}

	selector, err := labels.Parse(scale.Status.Selector)
	if err != nil {
		errMsg := fmt.Sprintf("couldn't convert selector into a corresponding internal selector object: %v", err)
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "InvalidSelector", errMsg)
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "InvalidSelector", errMsg)
		return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
	}
	specReplicas := scale.Spec.Replicas
	statusReplicas := scale.Status.Replicas
	statuses = make([]autoscaling.MetricStatus, len(metricSpecs))

	invalidMetricsCount := 0
	var invalidMetricError error
	var invalidMetricCondition autoscaling.GeneralPodAutoscalerCondition

	for i, metricSpec := range metricSpecs {
		replicaCountProposal, metricNameProposal, timestampProposal, condition, err := a.computeReplicasForCronMetric(gpa,
			metricSpec, specReplicas, statusReplicas, selector, &statuses[i])
		if err != nil {
			if invalidMetricsCount <= 0 {
				invalidMetricCondition = condition
				invalidMetricError = err
			}
			invalidMetricsCount++
		}
		if err == nil && (replicas == 0 || replicaCountProposal > replicas) {
			timestamp = timestampProposal
			replicas = replicaCountProposal
			metric = fmt.Sprintf("cron %s %s", scheduleName, metricNameProposal)
		}
	}

	// If all metrics are invalid return error and set condition on gpa based on first invalid metric.
	if invalidMetricsCount >= len(metricSpecs) {
		setCondition(gpa, invalidMetricCondition.Type, invalidMetricCondition.Status, invalidMetricCondition.Reason,
			invalidMetricCondition.Message)
		return 0, "", statuses, time.Time{}, fmt.Errorf("invalid metrics (%v invalid out of %v), "+
			"first error is: %v", invalidMetricsCount, len(metricSpecs), invalidMetricError)
	}
	setCondition(gpa, autoscaling.ScalingActive, v1.ConditionTrue, "ValidMetricFound",
		"the GPA was able to successfully calculate a replica count from %s", metric)
	return replicas, metric, statuses, timestamp, nil
}

// computeReplicasForSimple computes the desired number of replicas for the metric specifications listed in the GPA,
// returning the maximum  of the computed replica counts, a description of the associated metric, and the statuses of
// all metrics computed.
func (a *GeneralController) computeReplicasForSimple(gpa *autoscaling.GeneralPodAutoscaler,
	scale *autoscalinginternal.Scale) (replicas int32, metric string, statuses []autoscaling.MetricStatus,
	timestamp time.Time, err error) {
	if scale.Status.Selector == "" {
		errMsg := "selector is required"
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "SelectorRequired", errMsg)
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "InvalidSelector",
			"the GPA target's scale is missing a selector")
		return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
	}

	_, err = labels.Parse(scale.Status.Selector)
	if err != nil {
		errMsg := fmt.Sprintf("couldn't convert selector into a corresponding internal selector object: %v", err)
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "InvalidSelector", errMsg)
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "InvalidSelector", errMsg)
		return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
	}

	statusReplicas := scale.Status.Replicas

	replicaCountProposal, modeNameProposal, err := computeDesiredSize(gpa, a.buildScalerChain(gpa), statusReplicas)
	if err != nil {
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, fmt.Sprintf("%v failed", modeNameProposal),
			fmt.Sprintf("%v failed: %v",
				modeNameProposal, err))
		return 0, "", statuses, time.Time{}, fmt.Errorf("invalid mode %v, first error is: %v", modeNameProposal, err)
	}
	replicas = replicaCountProposal
	setCondition(gpa, autoscaling.ScalingActive, v1.ConditionTrue, "ValidMetricFound", "the GPA was able to successfully calculate a replica count from %s", metric)
	timestamp = time.Now()
	return replicas, modeNameProposal, statuses, timestamp, nil
}

// buildScalerChain build scaler chain for gpa scaler
func (a *GeneralController) buildScalerChain(gpa *autoscaling.GeneralPodAutoscaler) []scalercore.Scaler {
	var scalerChain []scalercore.Scaler
	if gpa.Spec.WebhookMode != nil {
		scalerChain = append(scalerChain, scalercore.NewWebhookScaler(gpa.Spec.WebhookMode))
	}
	if gpa.Spec.TimeMode != nil {
		scalerChain = append(scalerChain, scalercore.NewCronScaler(gpa.Spec.TimeMode.TimeRanges))
	}
	return scalerChain
}

// Computes the desired number of replicas for a specific gpa and metric specification,
// returning the metric status and a proposed condition to be set on the GPA object.
func (a *GeneralController) computeStatusForResourceMetricGeneric(currentReplicas int32, target autoscaling.MetricTarget,
	resourceName v1.ResourceName, namespace string, container string, selector labels.Selector, computeByLimits bool) (replicaCountProposal int32,
	metricStatus *autoscaling.MetricValueStatus, timestampProposal time.Time, metricNameProposal string,
	condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	if target.AverageValue != nil {
		var rawProposal int64
		replicaCountProposal, rawProposal, timestampProposal, err := a.replicaCalc.GetRawResourceReplicas(currentReplicas, target.AverageValue.MilliValue(), resourceName, namespace, selector, container)
		if err != nil {
			return 0, nil, time.Time{}, "", condition, fmt.Errorf("failed to get %s utilization: %v", resourceName, err)
		}
		metricNameProposal = fmt.Sprintf("%s resource", resourceName.String())
		status := autoscaling.MetricValueStatus{
			AverageValue: resource.NewMilliQuantity(rawProposal, resource.DecimalSI),
		}
		return replicaCountProposal, &status, timestampProposal, metricNameProposal, autoscaling.GeneralPodAutoscalerCondition{}, nil
	}

	if target.AverageUtilization == nil {
		errMsg := "invalid resource metric source: neither a utilization target nor a value target was set"
		return 0, nil, time.Time{}, "", condition, fmt.Errorf(errMsg)
	}

	targetUtilization := *target.AverageUtilization
	replicaCountProposal, percentageProposal, rawProposal, timestampProposal, err := a.replicaCalc.GetResourceReplicas(currentReplicas, targetUtilization, resourceName, namespace, selector, container, computeByLimits)
	if err != nil {
		return 0, nil, time.Time{}, "", condition, fmt.Errorf("failed to get %s utilization: %v", resourceName, err)
	}
	computeResourceUtilizationRatioBy := "request"
	if computeByLimits {
		computeResourceUtilizationRatioBy = "limit"
	}

	metricNameProposal = fmt.Sprintf("%s resource utilization (percentage of %s)", resourceName, computeResourceUtilizationRatioBy)
	status := autoscaling.MetricValueStatus{
		AverageUtilization: &percentageProposal,
		AverageValue:       resource.NewMilliQuantity(rawProposal, resource.DecimalSI),
	}
	return replicaCountProposal, &status, timestampProposal, metricNameProposal, autoscaling.GeneralPodAutoscalerCondition{}, nil
}

// Computes the desired number of replicas for a specific gpa and metric specification,
// returning the metric status and a proposed condition to be set on the GPA object.
func (a *GeneralController) computeReplicasForMetric(gpa *autoscaling.GeneralPodAutoscaler, spec autoscaling.MetricSpec,
	specReplicas, statusReplicas int32, selector labels.Selector, status *autoscaling.MetricStatus) (replicaCountProposal int32, metricNameProposal string,
	timestampProposal time.Time, condition autoscaling.GeneralPodAutoscalerCondition, err error) {

	switch spec.Type {
	case autoscaling.ObjectMetricSourceType:
		metricSelector, err := metav1.LabelSelectorAsSelector(spec.Object.Metric.Selector)
		if err != nil {
			condition := a.getUnableComputeReplicaCountCondition(gpa, "FailedGetObjectMetric", err)
			return 0, "", time.Time{}, condition, fmt.Errorf("failed to get object metric value: %v", err)
		}
		replicaCountProposal, timestampProposal, metricNameProposal, condition, err = a.computeStatusForObjectMetric(specReplicas, statusReplicas, spec, gpa, selector, status, metricSelector)
		if err != nil {
			return 0, "", time.Time{}, condition, fmt.Errorf("failed to get object metric value: %v", err)
		}
	case autoscaling.PodsMetricSourceType:
		metricSelector, err := metav1.LabelSelectorAsSelector(spec.Pods.Metric.Selector)
		if err != nil {
			condition := a.getUnableComputeReplicaCountCondition(gpa, "FailedGetPodsMetric", err)
			return 0, "", time.Time{}, condition, fmt.Errorf("failed to get pods metric value: %v", err)
		}
		replicaCountProposal, timestampProposal, metricNameProposal, condition, err = a.computeStatusForPodsMetric(specReplicas, spec, gpa, selector, status, metricSelector)
		if err != nil {
			return 0, "", time.Time{}, condition, fmt.Errorf("failed to get pods metric value: %v", err)
		}
	case autoscaling.ResourceMetricSourceType:
		replicaCountProposal, timestampProposal, metricNameProposal, condition, err = a.computeStatusForResourceMetric(specReplicas, spec, gpa, selector, status)
		if err != nil {
			return 0, "", time.Time{}, condition, err
		}
	case autoscaling.ContainerResourceMetricSourceType:
		replicaCountProposal, timestampProposal, metricNameProposal, condition, err = a.computeStatusForContainerResourceMetric(specReplicas, spec, gpa, selector, status)
		if err != nil {
			return 0, "", time.Time{}, condition, err
		}
	case autoscaling.ExternalMetricSourceType:
		replicaCountProposal, timestampProposal, metricNameProposal, condition, err = a.computeStatusForExternalMetric(specReplicas, statusReplicas, spec, gpa, selector, status)
		if err != nil {
			return 0, "", time.Time{}, condition, err
		}
	default:
		errMsg := fmt.Sprintf("unknown metric source type %q", string(spec.Type))
		err = fmt.Errorf(errMsg)
		condition := a.getUnableComputeReplicaCountCondition(gpa, "InvalidMetricSourceType", err)
		return 0, "", time.Time{}, condition, err
	}
	return replicaCountProposal, metricNameProposal, timestampProposal, autoscaling.GeneralPodAutoscalerCondition{}, nil
}

// Computes the desired number of replicas for a specific gpa and metric specification,
// returning the metric status and a proposed condition to be set on the GPA object.
func (a *GeneralController) computeReplicasForCronMetric(gpa *autoscaling.GeneralPodAutoscaler, spec autoscaling.CronMetricSpec,
	specReplicas, statusReplicas int32, selector labels.Selector, status *autoscaling.MetricStatus) (replicaCountProposal int32, metricNameProposal string,
	timestampProposal time.Time, condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	replicaCountProposal, metricNameProposal, timestampProposal, condition, err = a.computeReplicasForMetric(gpa, spec.MetricSpec, specReplicas, statusReplicas, selector, status)

	max := gpa.Spec.MaxReplicas
	min := *gpa.Spec.MinReplicas
	if replicaCountProposal < min {
		replicaCountProposal = min
	}
	if replicaCountProposal > max {
		replicaCountProposal = max
	}

	return replicaCountProposal, metricNameProposal, timestampProposal, condition, err
}

func (a *GeneralController) reconcileKey(key string) (deleted bool, err error) {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return true, err
	}

	gpa, err := a.gpaLister.GeneralPodAutoscalers(namespace).Get(name)
	if errors.IsNotFound(err) {
		klog.Infof("General Pod Autoscaler %s has been deleted in %s", name, namespace)
		delete(a.recommendations, key)
		delete(a.scaleUpEvents, key)
		delete(a.scaleDownEvents, key)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, a.reconcileAutoscaler(gpa, key)
}

// computeStatusForObjectMetric computes the desired number of replicas for the specified metric of type ObjectMetricSourceType.
func (a *GeneralController) computeStatusForObjectMetric(specReplicas, statusReplicas int32, metricSpec autoscaling.MetricSpec, gpa *autoscaling.GeneralPodAutoscaler, selector labels.Selector, status *autoscaling.MetricStatus, metricSelector labels.Selector) (replicas int32, timestamp time.Time, metricName string, condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	if metricSpec.Object.Target.Type == autoscaling.ValueMetricType {
		replicaCountProposal, utilizationProposal, timestampProposal, err := a.replicaCalc.GetObjectMetricReplicas(specReplicas, metricSpec.Object.Target.Value.MilliValue(), metricSpec.Object.Metric.Name, gpa.Namespace, &metricSpec.Object.DescribedObject, selector, metricSelector)
		if err != nil {
			condition := a.getUnableComputeReplicaCountCondition(gpa, "FailedGetObjectMetric", err)
			return 0, timestampProposal, "", condition, err
		}
		*status = autoscaling.MetricStatus{
			Type: autoscaling.ObjectMetricSourceType,
			Object: &autoscaling.ObjectMetricStatus{
				DescribedObject: metricSpec.Object.DescribedObject,
				Metric: autoscaling.MetricIdentifier{
					Name:     metricSpec.Object.Metric.Name,
					Selector: metricSpec.Object.Metric.Selector,
				},
				Current: autoscaling.MetricValueStatus{
					Value: resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
				},
			},
		}
		return replicaCountProposal, timestampProposal, fmt.Sprintf("%s metric %s", metricSpec.Object.DescribedObject.Kind, metricSpec.Object.Metric.Name), autoscaling.GeneralPodAutoscalerCondition{}, nil
	} else if metricSpec.Object.Target.Type == autoscaling.AverageValueMetricType {
		replicaCountProposal, utilizationProposal, timestampProposal, err := a.replicaCalc.GetObjectPerPodMetricReplicas(statusReplicas, metricSpec.Object.Target.AverageValue.MilliValue(), metricSpec.Object.Metric.Name, gpa.Namespace, &metricSpec.Object.DescribedObject, metricSelector)
		if err != nil {
			condition := a.getUnableComputeReplicaCountCondition(gpa, "FailedGetObjectMetric", err)
			return 0, time.Time{}, "", condition, fmt.Errorf("failed to get %s object metric: %v", metricSpec.Object.Metric.Name, err)
		}
		*status = autoscaling.MetricStatus{
			Type: autoscaling.ObjectMetricSourceType,
			Object: &autoscaling.ObjectMetricStatus{
				Metric: autoscaling.MetricIdentifier{
					Name:     metricSpec.Object.Metric.Name,
					Selector: metricSpec.Object.Metric.Selector,
				},
				Current: autoscaling.MetricValueStatus{
					AverageValue: resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
				},
			},
		}
		return replicaCountProposal, timestampProposal, fmt.Sprintf("external metric %s(%+v)", metricSpec.Object.Metric.Name, metricSpec.Object.Metric.Selector), autoscaling.GeneralPodAutoscalerCondition{}, nil
	}
	errMsg := "invalid object metric source: neither a value target nor an average value target was set"
	err = fmt.Errorf(errMsg)
	condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetObjectMetric", err)
	return 0, time.Time{}, "", condition, err
}

// computeStatusForPodsMetric computes the desired number of replicas for the specified metric of type PodsMetricSourceType.
func (a *GeneralController) computeStatusForPodsMetric(currentReplicas int32, metricSpec autoscaling.MetricSpec, gpa *autoscaling.GeneralPodAutoscaler, selector labels.Selector, status *autoscaling.MetricStatus, metricSelector labels.Selector) (replicaCountProposal int32, timestampProposal time.Time, metricNameProposal string, condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	replicaCountProposal, utilizationProposal, timestampProposal, err := a.replicaCalc.GetMetricReplicas(currentReplicas, metricSpec.Pods.Target.AverageValue.MilliValue(), metricSpec.Pods.Metric.Name, gpa.Namespace, selector, metricSelector)
	if err != nil {
		condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetPodsMetric", err)
		return 0, timestampProposal, "", condition, err
	}
	*status = autoscaling.MetricStatus{
		Type: autoscaling.PodsMetricSourceType,
		Pods: &autoscaling.PodsMetricStatus{
			Metric: autoscaling.MetricIdentifier{
				Name:     metricSpec.Pods.Metric.Name,
				Selector: metricSpec.Pods.Metric.Selector,
			},
			Current: autoscaling.MetricValueStatus{
				AverageValue: resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
			},
		},
	}

	return replicaCountProposal, timestampProposal, fmt.Sprintf("pods metric %s", metricSpec.Pods.Metric.Name), autoscaling.GeneralPodAutoscalerCondition{}, nil
}

// computeStatusForResourceMetric computes the desired number of replicas for the specified metric of type ResourceMetricSourceType.
func (a *GeneralController) computeStatusForResourceMetric(currentReplicas int32, metricSpec autoscaling.MetricSpec, gpa *autoscaling.GeneralPodAutoscaler, selector labels.Selector, status *autoscaling.MetricStatus) (replicaCountProposal int32, timestampProposal time.Time, metricNameProposal string, condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	if metricSpec.Resource.Target.AverageValue != nil {
		var rawProposal int64
		replicaCountProposal, rawProposal, timestampProposal, err := a.replicaCalc.GetRawResourceReplicas(currentReplicas, metricSpec.Resource.Target.AverageValue.MilliValue(), metricSpec.Resource.Name, gpa.Namespace, selector, "")
		if err != nil {
			condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetResourceMetric", err)
			return 0, time.Time{}, "", condition, fmt.Errorf("failed to get %s utilization: %v", metricSpec.Resource.Name, err)
		}
		metricNameProposal = fmt.Sprintf("%s resource", metricSpec.Resource.Name)
		*status = autoscaling.MetricStatus{
			Type: autoscaling.ResourceMetricSourceType,
			Resource: &autoscaling.ResourceMetricStatus{
				Name: metricSpec.Resource.Name,
				Current: autoscaling.MetricValueStatus{
					AverageValue: resource.NewMilliQuantity(rawProposal, resource.DecimalSI),
				},
			},
		}
		return replicaCountProposal, timestampProposal, metricNameProposal, autoscaling.GeneralPodAutoscalerCondition{}, nil
	}
	if metricSpec.Resource.Target.AverageUtilization == nil {
		errMsg := "invalid resource metric source: neither a utilization target nor a value target was set"
		err = fmt.Errorf(errMsg)
		condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetResourceMetric", err)
		return 0, time.Time{}, "", condition, fmt.Errorf(errMsg)
	}
	computeByLimits := isComputeByLimits(gpa)
	targetUtilization := *metricSpec.Resource.Target.AverageUtilization
	replicaCountProposal, percentageProposal, rawProposal, timestampProposal, err := a.replicaCalc.GetResourceReplicas(currentReplicas, targetUtilization, metricSpec.Resource.Name, gpa.Namespace, selector, "", computeByLimits)
	if err != nil {
		condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetResourceMetric", err)
		return 0, time.Time{}, "", condition, fmt.Errorf("failed to get %s utilization: %v", metricSpec.Resource.Name, err)
	}
	computeResourceUtilizationRatioBy := "request"
	if computeByLimits {
		computeResourceUtilizationRatioBy = "limit"
	}
	metricNameProposal = fmt.Sprintf("%s resource utilization (percentage of %s)", metricSpec.Resource.Name, computeResourceUtilizationRatioBy)
	*status = autoscaling.MetricStatus{
		Type: autoscaling.ResourceMetricSourceType,
		Resource: &autoscaling.ResourceMetricStatus{
			Name: metricSpec.Resource.Name,
			Current: autoscaling.MetricValueStatus{
				AverageUtilization: &percentageProposal,
				AverageValue:       resource.NewMilliQuantity(rawProposal, resource.DecimalSI),
			},
		},
	}
	return replicaCountProposal, timestampProposal, metricNameProposal, autoscaling.GeneralPodAutoscalerCondition{}, nil
}

// computeStatusForContainerResourceMetric computes the desired number of replicas for the specified metric of
// type ResourceMetricSourceType.
func (a *GeneralController) computeStatusForContainerResourceMetric(currentReplicas int32,
	metricSpec autoscaling.MetricSpec, gpa *autoscaling.GeneralPodAutoscaler,
	selector labels.Selector, status *autoscaling.MetricStatus) (replicaCountProposal int32, timestampProposal time.Time,
	metricNameProposal string, condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	computeByLimits := isComputeByLimits(gpa)
	replicaCountProposal, metricValueStatus, timestampProposal, metricNameProposal, condition, err := a.computeStatusForResourceMetricGeneric(currentReplicas, metricSpec.ContainerResource.Target, metricSpec.ContainerResource.Name, gpa.Namespace, metricSpec.ContainerResource.Container, selector, computeByLimits)
	if err != nil {
		condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetContainerResourceMetric", err)
		return replicaCountProposal, timestampProposal, metricNameProposal, condition, err
	}
	*status = autoscaling.MetricStatus{
		Type: autoscaling.ContainerResourceMetricSourceType,
		ContainerResource: &autoscaling.ContainerResourceMetricStatus{
			Name:      metricSpec.ContainerResource.Name,
			Container: metricSpec.ContainerResource.Container,
			Current:   *metricValueStatus,
		},
	}
	return replicaCountProposal, timestampProposal, metricNameProposal, condition, nil
}

// computeStatusForExternalMetric computes the desired number of replicas for the specified metric of type ExternalMetricSourceType.
func (a *GeneralController) computeStatusForExternalMetric(specReplicas, statusReplicas int32, metricSpec autoscaling.MetricSpec, gpa *autoscaling.GeneralPodAutoscaler, selector labels.Selector, status *autoscaling.MetricStatus) (replicaCountProposal int32, timestampProposal time.Time, metricNameProposal string, condition autoscaling.GeneralPodAutoscalerCondition, err error) {
	if metricSpec.External.Target.AverageValue != nil {
		replicaCountProposal, utilizationProposal, timestampProposal, err := a.replicaCalc.GetExternalPerPodMetricReplicas(statusReplicas,
			metricSpec.External.Target.AverageValue.MilliValue(), metricSpec.External.Metric.Name, gpa.Namespace, metricSpec.External.Metric.Selector)
		if err != nil {
			condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetExternalMetric", err)
			return 0, time.Time{}, "", condition,
				fmt.Errorf("failed to get %s external metric: %v", metricSpec.External.Metric.Name, err)
		}
		*status = autoscaling.MetricStatus{
			Type: autoscaling.ExternalMetricSourceType,
			External: &autoscaling.ExternalMetricStatus{
				Metric: autoscaling.MetricIdentifier{
					Name:     metricSpec.External.Metric.Name,
					Selector: metricSpec.External.Metric.Selector,
				},
				Current: autoscaling.MetricValueStatus{
					AverageValue: resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
				},
			},
		}
		return replicaCountProposal, timestampProposal, fmt.Sprintf("external metric %s(%+v)",
			metricSpec.External.Metric.Name, metricSpec.External.Metric.Selector), autoscaling.GeneralPodAutoscalerCondition{}, nil
	}
	if metricSpec.External.Target.Value != nil {
		replicaCountProposal, utilizationProposal, timestampProposal, err := a.replicaCalc.GetExternalMetricReplicas(specReplicas,
			metricSpec.External.Target.Value.MilliValue(), metricSpec.External.Metric.Name, gpa.Namespace, metricSpec.External.Metric.Selector, selector)
		if err != nil {
			condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetExternalMetric", err)
			return 0, time.Time{}, "", condition,
				fmt.Errorf("failed to get external metric %s: %v", metricSpec.External.Metric.Name, err)
		}
		*status = autoscaling.MetricStatus{
			Type: autoscaling.ExternalMetricSourceType,
			External: &autoscaling.ExternalMetricStatus{
				Metric: autoscaling.MetricIdentifier{
					Name:     metricSpec.External.Metric.Name,
					Selector: metricSpec.External.Metric.Selector,
				},
				Current: autoscaling.MetricValueStatus{
					Value: resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
				},
			},
		}
		return replicaCountProposal, timestampProposal, fmt.Sprintf("external metric %s(%+v)",
				metricSpec.External.Metric.Name, metricSpec.External.Metric.Selector),
			autoscaling.GeneralPodAutoscalerCondition{}, nil
	}
	errMsg := "invalid external metric source: neither a value target nor an average value target was set"
	err = fmt.Errorf(errMsg)
	condition = a.getUnableComputeReplicaCountCondition(gpa, "FailedGetExternalMetric", err)
	return 0, time.Time{}, "", condition, fmt.Errorf(errMsg)
}

func (a *GeneralController) recordInitialRecommendation(currentReplicas int32, key string) {
	//add lock
	a.recommendationsLock.Lock()
	defer a.recommendationsLock.Unlock()

	if a.recommendations[key] == nil {
		a.recommendations[key] = []timestampedRecommendation{{currentReplicas, time.Now()}}
	}
}

//reconcileAutoscaler
func (a *GeneralController) reconcileAutoscaler(gpa *autoscaling.GeneralPodAutoscaler, key string) error {
	// make a copy so that we never mutate the shared informer cache (conversion can mutate the object)
	gpaStatusOriginal := gpa.Status.DeepCopy()

	reference := fmt.Sprintf("%s/%s/%s", gpa.Spec.ScaleTargetRef.Kind, gpa.Namespace, gpa.Spec.ScaleTargetRef.Name)

	targetGV, err := schema.ParseGroupVersion(gpa.Spec.ScaleTargetRef.APIVersion)
	if err != nil {
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "FailedGetScale", err.Error())
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionFalse, "FailedGetScale",
			"the GPA controller was unable to get the target's current scale: %v", err)
		if updateErr := a.updateStatusIfNeeded(gpaStatusOriginal, gpa); updateErr != nil {
			klog.Error(updateErr)
		}
		return fmt.Errorf("invalid API version in scale target reference: %v", err)
	}

	targetGK := schema.GroupKind{
		Group: targetGV.Group,
		Kind:  gpa.Spec.ScaleTargetRef.Kind,
	}

	mappings, err := a.mapper.RESTMappings(targetGK)
	if err != nil {
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "FailedGetScale", err.Error())
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionFalse, "FailedGetScale",
			"the GPA controller was unable to get the target's current scale: %v", err)
		if updateErr := a.updateStatusIfNeeded(gpaStatusOriginal, gpa); updateErr != nil {
			klog.Error(updateErr)
		}
		return fmt.Errorf("unable to determine resource for scale target reference: %v", err)
	}

	scale, targetGR, err := a.scaleForResourceMappings(gpa.Namespace, gpa.Spec.ScaleTargetRef.Name, mappings)
	if err != nil {
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "FailedGetScale", err.Error())
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionFalse, "FailedGetScale",
			"the GPA controller was unable to get the target's current scale: %v", err)
		if updateErr := a.updateStatusIfNeeded(gpaStatusOriginal, gpa); updateErr != nil {
			klog.Error(updateErr)
		}
		return fmt.Errorf("failed to query scale subresource for %s: %v", reference, err)
	}
	if len(scale.Status.Selector) != 0 {
		// record selector accelerate search
		labelMap, err := labels.ConvertSelectorToLabelsMap(scale.Status.Selector)
		if err == nil {
			if err = a.updateLabelsIfNeeded(gpa, labelMap); err != nil {
				klog.Warningf("Add labels: %v to gpa: %v failed: %v", labelMap, gpa.Name, err)
			}
		} else {
			klog.Errorf("ConvertSelectorToLabelsMap: %v faield: %v", scale.Status.Selector, err)
		}
	}

	setCondition(gpa, autoscaling.AbleToScale, v1.ConditionTrue, "SucceededGetScale",
		"the GPA controller was able to get the target's current scale")
	currentReplicas := scale.Spec.Replicas
	a.recordInitialRecommendation(currentReplicas, key)

	var (
		metricStatuses        []autoscaling.MetricStatus
		metricDesiredReplicas int32
		metricName            string
	)

	desiredReplicas := int32(0)
	rescaleReason := ""

	var minReplicas int32
	var max, min int32
	var scheduleName string
	var cronMetricsScale *scalercore.CronMetricsScaler
	if gpa.Spec.CronMetricMode != nil {
		cronMetricsScale = scalercore.NewCronMetricsScaler(gpa.Spec.CronMetricMode.CronMetrics)
		max, min, scheduleName = cronMetricsScale.GetCurrentMaxAndMinReplicas(gpa)
		klog.Infof("current cron schedule: %s, max: %v, min: %v", scheduleName, max, min)
		gpa.Spec.MinReplicas = &min
		gpa.Spec.MaxReplicas = max
	}
	if gpa.Spec.MinReplicas != nil {
		minReplicas = *gpa.Spec.MinReplicas
	} else {
		// Default value
		minReplicas = 1
	}

	rescale := true
	if scale.Spec.Replicas == 0 && minReplicas != 0 {
		// Autoscaling is disabled for this resource
		desiredReplicas = 0
		rescale = false
		setCondition(gpa, autoscaling.ScalingActive, v1.ConditionFalse, "ScalingDisabled",
			"scaling is disabled since the replica count of the target is zero")
	} else if currentReplicas > gpa.Spec.MaxReplicas {
		rescaleReason = "Current number of replicas above Spec.MaxReplicas"
		desiredReplicas = gpa.Spec.MaxReplicas
	} else if currentReplicas < minReplicas {
		rescaleReason = "Current number of replicas below Spec.MinReplicas"
		desiredReplicas = minReplicas
	} else {
		var metricTimestamp time.Time
		if isEmpty(gpa.Spec.AutoScalingDrivenMode) {
			return nil
		}
		switch {
		case gpa.Spec.MetricMode != nil:
			metricDesiredReplicas, metricName, metricStatuses, metricTimestamp, err = a.computeReplicasForMetrics(gpa,
				scale, gpa.Spec.MetricMode.Metrics)
		case gpa.Spec.CronMetricMode != nil:
			CronMetrics := cronMetricsScale.GetCurrentCronMetricSpecs(gpa, scheduleName)
			metricDesiredReplicas, metricName, metricStatuses, metricTimestamp, err = a.computeReplicasForCronMetrics(gpa,
				scale, CronMetrics, scheduleName)
		default:
			metricDesiredReplicas, metricName, metricStatuses, metricTimestamp, err = a.computeReplicasForSimple(gpa,
				scale)
		}
		if err != nil {
			a.setCurrentReplicasInStatus(gpa, currentReplicas)
			if err := a.updateStatusIfNeeded(gpaStatusOriginal, gpa); err != nil {
				utilruntime.HandleError(err)
			}
			a.eventRecorder.Event(gpa, v1.EventTypeWarning, "FailedComputeMetricsReplicas", err.Error())
			return fmt.Errorf("failed to compute desired number of replicas based on listed metrics for %s: %v", reference, err)
		}
		//Record event when the metricDesiredReplicas is greater than gpa.Spec.MaxReplicas
		if metricDesiredReplicas > gpa.Spec.MaxReplicas {
			a.eventRecorder.Eventf(gpa, v1.EventTypeWarning, "FailedRescale", "DesiredReplicas:%v cannot exceed the MaxReplicas: %v", metricDesiredReplicas, gpa.Spec.MaxReplicas)
		}
		klog.V(4).Infof("proposing %v desired replicas (based on %s from %s) for %s",
			metricDesiredReplicas, metricName, metricTimestamp, reference)
		rescaleMetric := ""
		if metricDesiredReplicas > desiredReplicas {
			desiredReplicas = metricDesiredReplicas
			rescaleMetric = metricName
		}
		if desiredReplicas > currentReplicas {
			rescaleReason = fmt.Sprintf("%s above target", rescaleMetric)
		}
		if desiredReplicas < currentReplicas {
			rescaleReason = "All metrics below target"
		}
		if gpa.Spec.Behavior == nil {
			desiredReplicas = a.normalizeDesiredReplicas(gpa, key, currentReplicas, desiredReplicas, minReplicas)
		} else {
			klog.V(4).Infof("%s start behaviors", gpa.Name)
			desiredReplicas = a.normalizeDesiredReplicasWithBehaviors(gpa, key, currentReplicas, desiredReplicas, minReplicas)
		}
		klog.V(4).Infof("desire: %v, current: %v, min: %v, max: %v",
			desiredReplicas, currentReplicas, minReplicas, gpa.Spec.MaxReplicas)
		rescale = desiredReplicas != currentReplicas
	}

	if rescale {
		//if desiredReplicas is 0, skip to update replicas and send event
		if desiredReplicas == 0 {
			a.eventRecorder.Eventf(gpa, v1.EventTypeWarning, "FailedRescale",
				"desiredReplicas: %d; reason: %s; skip modify replicas", desiredReplicas, rescaleReason)
			return fmt.Errorf("failed to rescale %s: desiredReplicas=0 skip modify replcias", reference)
		}
		scale.Spec.Replicas = desiredReplicas
		klog.Infof("rescale for %s, scale info: %v", reference, scale)
		_, err = a.scaleNamespacer.Scales(gpa.Namespace).Update(targetGR, scale)
		if err != nil {
			a.eventRecorder.Eventf(gpa, v1.EventTypeWarning, "FailedRescale",
				"New size: %d; reason: %s; error: %v", desiredReplicas, rescaleReason, err.Error())
			setCondition(gpa, autoscaling.AbleToScale, v1.ConditionFalse, "FailedUpdateScale",
				"the GPA controller was unable to update the target scale: %v", err)
			a.setCurrentReplicasInStatus(gpa, currentReplicas)
			if err := a.updateStatusIfNeeded(gpaStatusOriginal, gpa); err != nil {
				utilruntime.HandleError(err)
			}
			return fmt.Errorf("failed to rescale %s: %v", reference, err)
		}
		// calculatePodResources
		var (
			cpuRequests, cpuLimits, memRequests, memLimits float32
			_err                                           error
		)
		cpuRequests, cpuLimits, memRequests, memLimits, _err = a.calculateOnePodResources(gpa.Namespace, scale.Status.Selector)
		if _err != nil {
			klog.Errorf("calculateOnePodResources error:%v", _err)
		}
		changeReplicas := float32(desiredReplicas - currentReplicas)
		scaleEvt := ScaleEvent{
			OldReplicas:          currentReplicas,
			NewReplicas:          desiredReplicas,
			MinReplicas:          *gpa.Spec.MinReplicas,
			MaxReplicas:          gpa.Spec.MaxReplicas,
			CpuRequestsOfChanges: changeReplicas * cpuRequests,
			CpuLimitsOfChanges:   changeReplicas * cpuLimits,
			MemRequestsOfChanges: changeReplicas * memRequests,
			MemLimitsOfChanges:   changeReplicas * memLimits,
			Reason:               rescaleReason,
		}
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionTrue,
			"SucceededRescale", "the GPA controller was able to update the target scale to %d", desiredReplicas)

		a.storeScaleEvent(gpa.Spec.Behavior, key, currentReplicas, desiredReplicas)
		bytes, err := json.Marshal(scaleEvt)
		if err != nil {
			a.eventRecorder.Eventf(gpa, v1.EventTypeNormal, "SuccessfulRescale",
				"old size: %d; new size: %d; min size: %d; max size: %d; reason: %s", currentReplicas, desiredReplicas, *gpa.Spec.MinReplicas, gpa.Spec.MaxReplicas, rescaleReason)
		} else {
			a.eventRecorder.Eventf(gpa, v1.EventTypeNormal, "SuccessfulRescale", string(bytes))
		}
		klog.Infof("Successful rescale of %s, old size: %d, new size: %d, reason: %s",
			gpa.Name, currentReplicas, desiredReplicas, rescaleReason)
	} else {
		klog.V(4).Infof("decided not to scale %s to %v (last scale time was %s)",
			reference, desiredReplicas, gpa.Status.LastScaleTime)
		desiredReplicas = currentReplicas
	}
	a.setStatus(gpa, currentReplicas, desiredReplicas, metricStatuses, rescale)
	return a.updateStatusIfNeeded(gpaStatusOriginal, gpa)
}

//calculateOnePodResources
func (a *GeneralController) calculateOnePodResources(namespace, selectorStr string) (float32, float32, float32, float32, error) {
	selector, err := labels.Parse(selectorStr)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	podList, err := a.podLister.Pods(namespace).List(selector)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if podList != nil && len(podList) > 0 {
		pod := podList[0]
		var cpuRequests, cpuLimits, memRequests, memLimits int64
		var totalCpuRequests, totalCpuLimits, totalMemRequests, totalMemLimits float32
		for _, c := range pod.Spec.Containers {
			cpuRequests += c.Resources.Requests.Cpu().MilliValue()
			cpuLimits += c.Resources.Limits.Cpu().MilliValue()
			memRequests += c.Resources.Requests.Memory().Value()
			memLimits += c.Resources.Limits.Memory().Value()
		}
		totalCpuRequests = float32(cpuRequests)
		totalCpuLimits = float32(cpuLimits)
		totalMemRequests = float32(memRequests / 1024 / 1024)
		totalMemLimits = float32(memLimits / 1024 / 1024)
		return totalCpuRequests, totalCpuLimits, totalMemRequests, totalMemLimits, nil
	}
	return 0, 0, 0, 0, nil
}

func (a *GeneralController) updateLabelsIfNeeded(gpa *autoscaling.GeneralPodAutoscaler, labelMap map[string]string) error {
	if len(labelMap) == 0 {
		return nil
	}
	currentLabels := gpa.Labels
	if currentLabels == nil {
		currentLabels = map[string]string{}
	}
	for k, v := range labelMap {
		currentLabels[k] = v
	}
	if reflect.DeepEqual(currentLabels, gpa.Labels) {
		return nil
	}
	gpaCopy := gpa.DeepCopy()
	gpaCopy.Labels = currentLabels
	patch, err := util.CreateMergePatch(gpa, gpaCopy)
	if err != nil {
		return err
	}
	if apiequality.Semantic.DeepEqual(gpa, gpaCopy) {
		return nil
	}
	gpaCopy, err = a.gpaNamespacer.GeneralPodAutoscalers(gpa.Namespace).Patch(gpa.Name, types.MergePatchType, patch)
	if err == nil {
		gpa = gpaCopy
		return nil
	}
	klog.Errorf("patch gpa: %v error: %v", gpa.Name, err)
	return err
}

// stabilizeRecommendation:
// - replaces old recommendation with the newest recommendation,
// - returns max of recommendations that are not older than downscaleStabilisationWindow.
func (a *GeneralController) stabilizeRecommendation(key string, prenormalizedDesiredReplicas int32) int32 {
	//add lock
	a.recommendationsLock.Lock()
	defer a.recommendationsLock.Unlock()

	maxRecommendation := prenormalizedDesiredReplicas
	foundOldSample := false
	oldSampleIndex := 0
	cutoff := time.Now().Add(-a.downscaleStabilisationWindow)
	for i, rec := range a.recommendations[key] {
		if rec.timestamp.Before(cutoff) {
			foundOldSample = true
			oldSampleIndex = i
		} else if rec.recommendation > maxRecommendation {
			maxRecommendation = rec.recommendation
		}
	}
	if foundOldSample {
		a.recommendations[key][oldSampleIndex] = timestampedRecommendation{
			prenormalizedDesiredReplicas, time.Now()}
	} else {
		a.recommendations[key] = append(a.recommendations[key], timestampedRecommendation{
			prenormalizedDesiredReplicas, time.Now()})
	}
	return maxRecommendation
}

// normalizeDesiredReplicas takes the metrics desired replicas value and normalizes it based on the appropriate conditions (i.e. < maxReplicas, >
// minReplicas, etc...)
func (a *GeneralController) normalizeDesiredReplicas(gpa *autoscaling.GeneralPodAutoscaler,
	key string, currentReplicas int32, prenormalizedDesiredReplicas int32, minReplicas int32) int32 {
	stabilizedRecommendation := a.stabilizeRecommendation(key, prenormalizedDesiredReplicas)
	if stabilizedRecommendation != prenormalizedDesiredReplicas {
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionTrue, "ScaleDownStabilized",
			"recent recommendations were higher than current one, applying the highest recent recommendation")
	} else {
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionTrue, "ReadyForNewScale",
			"recommended size matches current size")
	}

	desiredReplicas, condition, reason := convertDesiredReplicasWithRules(currentReplicas,
		stabilizedRecommendation, minReplicas, gpa.Spec.MaxReplicas)

	if desiredReplicas == stabilizedRecommendation {
		setCondition(gpa, autoscaling.ScalingLimited, v1.ConditionFalse, condition, reason)
	} else {
		setCondition(gpa, autoscaling.ScalingLimited, v1.ConditionTrue, condition, reason)
	}

	return desiredReplicas
}

// NormalizationArg is used to pass all needed information between functions as one structure
type NormalizationArg struct {
	Key               string
	ScaleUpBehavior   *autoscaling.GPAScalingRules
	ScaleDownBehavior *autoscaling.GPAScalingRules
	MinReplicas       int32
	MaxReplicas       int32
	CurrentReplicas   int32
	DesiredReplicas   int32
}

// normalizeDesiredReplicasWithBehaviors takes the metrics desired replicas value and normalizes it:
// 1. Apply the basic conditions (i.e. < maxReplicas, > minReplicas, etc...)
// 2. Apply the scale up/down limits from the gpaSpec.Behaviors (i.e. add no more than 4 pods)
// 3. Apply the constraints period (i.e. add no more than 4 pods per minute)
// 4. Apply the stabilization (i.e. add no more than 4 pods per minute, and pick the smallest
//    recommendation during last 5 minutes)
func (a *GeneralController) normalizeDesiredReplicasWithBehaviors(gpa *autoscaling.GeneralPodAutoscaler,
	key string, currentReplicas, prenormalizedDesiredReplicas, minReplicas int32) int32 {
	a.maybeInitScaleDownStabilizationWindow(gpa)
	normalizationArg := NormalizationArg{
		Key:               key,
		ScaleUpBehavior:   gpa.Spec.Behavior.ScaleUp,
		ScaleDownBehavior: gpa.Spec.Behavior.ScaleDown,
		MinReplicas:       minReplicas,
		MaxReplicas:       gpa.Spec.MaxReplicas,
		CurrentReplicas:   currentReplicas,
		DesiredReplicas:   prenormalizedDesiredReplicas}
	stabilizedRecommendation, reason, message := a.stabilizeRecommendationWithBehaviors(normalizationArg)
	normalizationArg.DesiredReplicas = stabilizedRecommendation
	if stabilizedRecommendation != prenormalizedDesiredReplicas {
		// "ScaleUpStabilized" || "ScaleDownStabilized"
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionTrue, reason, message)
	} else {
		setCondition(gpa, autoscaling.AbleToScale, v1.ConditionTrue, "ReadyForNewScale",
			"recommended size matches current size")
	}
	desiredReplicas, reason, message := a.convertDesiredReplicasWithBehaviorRate(normalizationArg)
	if desiredReplicas == stabilizedRecommendation {
		setCondition(gpa, autoscaling.ScalingLimited, v1.ConditionFalse, reason, message)
	} else {
		setCondition(gpa, autoscaling.ScalingLimited, v1.ConditionTrue, reason, message)
	}

	return desiredReplicas
}

func (a *GeneralController) maybeInitScaleDownStabilizationWindow(gpa *autoscaling.GeneralPodAutoscaler) {
	behavior := gpa.Spec.Behavior
	if behavior != nil && behavior.ScaleDown != nil && behavior.ScaleDown.StabilizationWindowSeconds == nil {
		stabilizationWindowSeconds := (int32)(a.downscaleStabilisationWindow.Seconds())
		gpa.Spec.Behavior.ScaleDown.StabilizationWindowSeconds = &stabilizationWindowSeconds
	}
}

// getReplicasChangePerPeriod function find all the replica changes per period
func getReplicasChangePerPeriod(periodSeconds int32, scaleEvents []timestampedScaleEvent) int32 {
	period := time.Second * time.Duration(periodSeconds)
	cutoff := time.Now().Add(-period)
	var replicas int32
	for _, rec := range scaleEvents {
		if rec.timestamp.After(cutoff) {
			replicas += rec.replicaChange
		}
	}
	return replicas

}

func (a *GeneralController) getUnableComputeReplicaCountCondition(gpa *autoscaling.GeneralPodAutoscaler,
	reason string, err error) (condition autoscaling.GeneralPodAutoscalerCondition) {
	a.eventRecorder.Event(gpa, v1.EventTypeWarning, reason, err.Error())
	return autoscaling.GeneralPodAutoscalerCondition{
		Type:    autoscaling.ScalingActive,
		Status:  v1.ConditionFalse,
		Reason:  reason,
		Message: fmt.Sprintf("the GPA was unable to compute the replica count: %v", err),
	}
}

// storeScaleEvent stores (adds or replaces outdated) scale event.
// outdated events to be replaced were marked as outdated in the `markScaleEventsOutdated` function
func (a *GeneralController) storeScaleEvent(behavior *autoscaling.GeneralPodAutoscalerBehavior,
	key string, prevReplicas, newReplicas int32) {
	if behavior == nil {
		return // we should not store any event as they will not be used
	}
	var oldSampleIndex int
	var longestPolicyPeriod int32
	foundOldSample := false
	if newReplicas > prevReplicas {
		a.scaleUpEventsLock.Lock()
		defer a.scaleUpEventsLock.Unlock()

		longestPolicyPeriod = getLongestPolicyPeriod(behavior.ScaleUp)
		markScaleEventsOutdated(a.scaleUpEvents[key], longestPolicyPeriod)
		replicaChange := newReplicas - prevReplicas
		for i, event := range a.scaleUpEvents[key] {
			if event.outdated {
				foundOldSample = true
				oldSampleIndex = i
			}
		}
		newEvent := timestampedScaleEvent{replicaChange, time.Now(), false}
		if foundOldSample {
			a.scaleUpEvents[key][oldSampleIndex] = newEvent
		} else {
			a.scaleUpEvents[key] = append(a.scaleUpEvents[key], newEvent)
		}
	} else {
		a.scaleDownEventsLock.Lock()
		defer a.scaleDownEventsLock.Unlock()

		longestPolicyPeriod = getLongestPolicyPeriod(behavior.ScaleDown)
		markScaleEventsOutdated(a.scaleDownEvents[key], longestPolicyPeriod)
		replicaChange := prevReplicas - newReplicas
		for i, event := range a.scaleDownEvents[key] {
			if event.outdated {
				foundOldSample = true
				oldSampleIndex = i
			}
		}
		newEvent := timestampedScaleEvent{replicaChange, time.Now(), false}
		if foundOldSample {
			a.scaleDownEvents[key][oldSampleIndex] = newEvent
		} else {
			a.scaleDownEvents[key] = append(a.scaleDownEvents[key], newEvent)
		}
	}
}

// stabilizeRecommendationWithBehaviors:
// - replaces old recommendation with the newest recommendation,
// - returns {max,min} of recommendations that are not older than constraints.Scale{Up,Down}.DelaySeconds
func (a *GeneralController) stabilizeRecommendationWithBehaviors(args NormalizationArg) (int32, string, string) {
	recommendation := args.DesiredReplicas
	foundOldSample := false
	oldSampleIndex := 0
	var scaleDelaySeconds int32
	var reason, message string

	var betterRecommendation func(int32, int32) int32

	if args.DesiredReplicas >= args.CurrentReplicas {
		scaleDelaySeconds = *args.ScaleUpBehavior.StabilizationWindowSeconds
		betterRecommendation = min
		reason = "ScaleUpStabilized"
		message = "recent recommendations were lower than current one, applying the lowest recent recommendation"
	} else {
		scaleDelaySeconds = *args.ScaleDownBehavior.StabilizationWindowSeconds
		betterRecommendation = max
		reason = "ScaleDownStabilized"
		message = "recent recommendations were higher than current one, applying the highest recent recommendation"
	}

	maxDelaySeconds := max(*args.ScaleUpBehavior.StabilizationWindowSeconds, *args.ScaleDownBehavior.StabilizationWindowSeconds)
	obsoleteCutoff := time.Now().Add(-time.Second * time.Duration(maxDelaySeconds))

	cutoff := time.Now().Add(-time.Second * time.Duration(scaleDelaySeconds))
	for i, rec := range a.recommendations[args.Key] {
		if rec.timestamp.After(cutoff) {
			recommendation = betterRecommendation(rec.recommendation, recommendation)
		}
		if rec.timestamp.Before(obsoleteCutoff) {
			foundOldSample = true
			oldSampleIndex = i
		}
	}
	if foundOldSample {
		a.recommendations[args.Key][oldSampleIndex] = timestampedRecommendation{args.DesiredReplicas, time.Now()}
	} else {
		a.recommendations[args.Key] = append(a.recommendations[args.Key], timestampedRecommendation{args.DesiredReplicas, time.Now()})
	}
	return recommendation, reason, message
}

// convertDesiredReplicasWithBehaviorRate performs the actual normalization, given the constraint rate
// It doesn't consider the stabilizationWindow, it is done separately
func (a *GeneralController) convertDesiredReplicasWithBehaviorRate(args NormalizationArg) (int32, string, string) {
	var possibleLimitingReason, possibleLimitingMessage string
	if args.DesiredReplicas > args.CurrentReplicas && args.ScaleUpBehavior != nil {
		a.scaleUpEventsLock.Lock()
		defer a.scaleUpEventsLock.Unlock()

		scaleUpLimit := calculateScaleUpLimitWithScalingRules(args.CurrentReplicas,
			a.scaleUpEvents[args.Key], args.ScaleUpBehavior)
		if scaleUpLimit < args.CurrentReplicas {
			// We shouldn't scale up further until the scaleUpEvents will be cleaned up
			scaleUpLimit = args.CurrentReplicas
		}
		maximumAllowedReplicas := args.MaxReplicas
		if maximumAllowedReplicas > scaleUpLimit {
			maximumAllowedReplicas = scaleUpLimit
			possibleLimitingReason = "ScaleUpLimit"
			possibleLimitingMessage = "the desired replica count is increasing faster than the maximum scale rate"
		} else {
			possibleLimitingReason = "TooManyReplicas"
			possibleLimitingMessage = "the desired replica count is more than the maximum replica count"
		}
		if args.DesiredReplicas > maximumAllowedReplicas {
			return maximumAllowedReplicas, possibleLimitingReason, possibleLimitingMessage
		}
	} else if args.DesiredReplicas < args.CurrentReplicas && args.ScaleDownBehavior != nil {
		a.scaleDownEventsLock.Lock()
		defer a.scaleDownEventsLock.Unlock()

		scaleDownLimit := calculateScaleDownLimitWithBehaviors(args.CurrentReplicas,
			a.scaleDownEvents[args.Key], args.ScaleDownBehavior)
		if scaleDownLimit > args.CurrentReplicas {
			// We shouldn't scale down further until the scaleDownEvents will be cleaned up
			scaleDownLimit = args.CurrentReplicas
		}
		minimumAllowedReplicas := args.MinReplicas
		if minimumAllowedReplicas < scaleDownLimit {
			minimumAllowedReplicas = scaleDownLimit
			possibleLimitingReason = "ScaleDownLimit"
			possibleLimitingMessage = "the desired replica count is decreasing faster than the maximum scale rate"
		} else {
			possibleLimitingMessage = "the desired replica count is less than the minimum replica count"
			possibleLimitingReason = "TooFewReplicas"
		}
		if args.DesiredReplicas < minimumAllowedReplicas {
			return minimumAllowedReplicas, possibleLimitingReason, possibleLimitingMessage
		}
	}
	return args.DesiredReplicas, "DesiredWithinRange", "the desired count is within the acceptable range"
}

// computeDesiredSize computes the new desired size of the given fleet
func computeDesiredSize(gpa *autoscaling.GeneralPodAutoscaler,
	scalers []scalercore.Scaler, currentReplicas int32) (int32, string, error) {
	var (
		replicas int32
		errs     error
		name     string
	)
	klog.V(4).Infof("Scaler number of %v: %v", gpa.Name, len(scalers))
	for _, s := range scalers {
		chainReplicas, err := s.GetReplicas(gpa, currentReplicas)
		if err != nil {
			klog.Error(err)
			errs = pkgerrors.Wrap(err, fmt.Sprintf("GPA: %v get replicas error when call %v", gpa.Name, s.ScalerName()))
			continue
		}
		klog.V(4).Infof("GPA: %v scaler: %v, suggested replicas: %v", gpa.Name, s.ScalerName(), chainReplicas)
		if chainReplicas > replicas {
			replicas = chainReplicas
			name = s.ScalerName()
		}
	}

	return replicas, name, errs
}

// convertDesiredReplicas performs the actual normalization,
// without depending on `GeneralController` or `GeneralPodAutoscaler`
func convertDesiredReplicasWithRules(currentReplicas, desiredReplicas,
	gpaMinReplicas, gpaMaxReplicas int32) (int32, string, string) {
	var minimumAllowedReplicas int32
	var maximumAllowedReplicas int32

	var possibleLimitingCondition string
	var possibleLimitingReason string

	minimumAllowedReplicas = gpaMinReplicas

	// Do not upscale too much to prevent incorrect rapid increase of the number of master replicas caused by
	// bogus CPU usage report from heapster/kubelet (like in issue #32304).
	scaleUpLimit := calculateScaleUpLimit(currentReplicas)

	if gpaMaxReplicas > scaleUpLimit {
		maximumAllowedReplicas = scaleUpLimit
		possibleLimitingCondition = "ScaleUpLimit"
		possibleLimitingReason = "the desired replica count is increasing faster than the maximum scale rate"
	} else {
		maximumAllowedReplicas = gpaMaxReplicas
		possibleLimitingCondition = "TooManyReplicas"
		possibleLimitingReason = "the desired replica count is more than the maximum replica count"
	}

	if desiredReplicas < minimumAllowedReplicas {
		possibleLimitingCondition = "TooFewReplicas"
		possibleLimitingReason = "the desired replica count is less than the minimum replica count"

		return minimumAllowedReplicas, possibleLimitingCondition, possibleLimitingReason
	} else if desiredReplicas > maximumAllowedReplicas {
		return maximumAllowedReplicas, possibleLimitingCondition, possibleLimitingReason
	}

	return desiredReplicas, "DesiredWithinRange", "the desired count is within the acceptable range"
}

func calculateScaleUpLimit(currentReplicas int32) int32 {
	return int32(math.Max(scaleUpLimitFactor*float64(currentReplicas), scaleUpLimitMinimum))
}

// markScaleEventsOutdated set 'outdated=true' flag for all scale events that are not used by any GPA object
func markScaleEventsOutdated(scaleEvents []timestampedScaleEvent, longestPolicyPeriod int32) {
	period := time.Second * time.Duration(longestPolicyPeriod)
	cutoff := time.Now().Add(-period)
	for i, event := range scaleEvents {
		if event.timestamp.Before(cutoff) {
			// outdated scale event are marked for later reuse
			scaleEvents[i].outdated = true
		}
	}
}

func getLongestPolicyPeriod(scalingRules *autoscaling.GPAScalingRules) int32 {
	var longestPolicyPeriod int32
	for _, policy := range scalingRules.Policies {
		if policy.PeriodSeconds > longestPolicyPeriod {
			longestPolicyPeriod = policy.PeriodSeconds
		}
	}
	return longestPolicyPeriod
}

// calculateScaleUpLimitWithScalingRules returns the maximum number of pods
// that could be added for the given GPAScalingRules
func calculateScaleUpLimitWithScalingRules(currentReplicas int32, scaleEvents []timestampedScaleEvent,
	scalingRules *autoscaling.GPAScalingRules) int32 {
	var result int32
	var proposed int32
	var selectPolicyFn func(int32, int32) int32
	if *scalingRules.SelectPolicy == autoscaling.DisabledPolicySelect {
		return currentReplicas // Scaling is disabled
	} else if *scalingRules.SelectPolicy == autoscaling.MinPolicySelect {
		selectPolicyFn = min // For scaling up, the lowest change ('min' policy) produces a minimum value
	} else {
		selectPolicyFn = max // Use the default policy otherwise to produce a highest possible change
	}
	for _, policy := range scalingRules.Policies {
		replicasAddedInCurrentPeriod := getReplicasChangePerPeriod(policy.PeriodSeconds, scaleEvents)
		periodStartReplicas := currentReplicas - replicasAddedInCurrentPeriod
		if policy.Type == autoscaling.PodsScalingPolicy {
			proposed = int32(periodStartReplicas + policy.Value)
		} else if policy.Type == autoscaling.PercentScalingPolicy {
			// the proposal has to be rounded up because the proposed change might not increase the replica count
			// causing the target to never scale up
			proposed = int32(math.Ceil(float64(periodStartReplicas) * (1 + float64(policy.Value)/100)))
		}
		result = selectPolicyFn(result, proposed)
	}
	return result
}

// calculateScaleDownLimitWithBehavior returns the maximum number of pods
// that could be deleted for the given GPAScalingRules
func calculateScaleDownLimitWithBehaviors(currentReplicas int32, scaleEvents []timestampedScaleEvent,
	scalingRules *autoscaling.GPAScalingRules) int32 {
	var result int32 = math.MaxInt32
	var proposed int32
	var selectPolicyFn func(int32, int32) int32
	if *scalingRules.SelectPolicy == autoscaling.DisabledPolicySelect {
		return currentReplicas // Scaling is disabled
	} else if *scalingRules.SelectPolicy == autoscaling.MinPolicySelect {
		selectPolicyFn = max // For scaling down, the lowest change ('min' policy) produces a maximum value
	} else {
		selectPolicyFn = min // Use the default policy otherwise to produce a highest possible change
	}
	for _, policy := range scalingRules.Policies {
		replicasDeletedInCurrentPeriod := getReplicasChangePerPeriod(policy.PeriodSeconds, scaleEvents)
		periodStartReplicas := currentReplicas + replicasDeletedInCurrentPeriod
		if policy.Type == autoscaling.PodsScalingPolicy {
			proposed = periodStartReplicas - policy.Value
		} else if policy.Type == autoscaling.PercentScalingPolicy {
			proposed = int32(float64(periodStartReplicas) * (1 - float64(policy.Value)/100))
		}
		result = selectPolicyFn(result, proposed)
	}
	return result
}

// scaleForResourceMappings attempts to fetch the scale for the
// resource with the given name and namespace, trying each RESTMapping
// in turn until a working one is found.  If none work, the first error
// is returned.  It returns both the scale, as well as the group-resource from
// the working mapping.
func (a *GeneralController) scaleForResourceMappings(namespace, name string,
	mappings []*apimeta.RESTMapping) (*autoscalinginternal.Scale, schema.GroupResource, error) {
	var firstErr error
	for i, mapping := range mappings {
		targetGR := mapping.Resource.GroupResource()
		scale, err := a.scaleNamespacer.Scales(namespace).Get(targetGR, name)
		if err == nil {
			return scale, targetGR, nil
		}

		// if this is the first error, remember it,
		// then go on and try other mappings until we find a good one
		if i == 0 {
			firstErr = err
		}
	}

	// make sure we handle an empty set of mappings
	if firstErr == nil {
		firstErr = fmt.Errorf("unrecognized resource")
	}

	return nil, schema.GroupResource{}, firstErr
}

// setCurrentReplicasInStatus sets the current replica count in the status of the GPA.
func (a *GeneralController) setCurrentReplicasInStatus(gpa *autoscaling.GeneralPodAutoscaler, currentReplicas int32) {
	a.setStatus(gpa, currentReplicas, gpa.Status.DesiredReplicas, gpa.Status.CurrentMetrics, false)
}

// setStatus recreates the status of the given GPA, updating the current and
// desired replicas, as well as the metric statuses
func (a *GeneralController) setStatus(gpa *autoscaling.GeneralPodAutoscaler, currentReplicas,
	desiredReplicas int32, metricStatuses []autoscaling.MetricStatus, rescale bool) {
	gpa.Status = autoscaling.GeneralPodAutoscalerStatus{
		CurrentReplicas: currentReplicas,
		DesiredReplicas: desiredReplicas,
		LastScaleTime:   gpa.Status.LastScaleTime,
		CurrentMetrics:  metricStatuses,
		Conditions:      gpa.Status.Conditions,
	}
	now := metav1.NewTime(time.Now())
	if rescale {
		if gpa.Spec.TimeMode != nil || gpa.Spec.CronMetricMode != nil {
			gpa.Status.LastCronScheduleTime = &now
		}
		gpa.Status.LastScaleTime = &now
	}
}

// updateStatusIfNeeded calls updateStatus only if the status of the new GPA is not the same as the old status
func (a *GeneralController) updateStatusIfNeeded(oldStatus *autoscaling.GeneralPodAutoscalerStatus,
	newGPA *autoscaling.GeneralPodAutoscaler) error {
	// skip a write if we wouldn't need to update
	if apiequality.Semantic.DeepEqual(oldStatus, &newGPA.Status) {
		return nil
	}
	err := a.updateStatus(newGPA)
	if err == nil {
		return nil
	}
	_, err = a.gpaNamespacer.GeneralPodAutoscalers(newGPA.Namespace).Update(newGPA)
	return err
}

// updateStatus actually does the update request for the status of the given GPA
func (a *GeneralController) updateStatus(gpa *autoscaling.GeneralPodAutoscaler) error {
	_, err := a.gpaNamespacer.GeneralPodAutoscalers(gpa.Namespace).UpdateStatus(gpa)
	if err != nil {
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "FailedUpdateStatus", err.Error())
		return fmt.Errorf("failed to update status for %s: %v", gpa.Name, err)
	}
	klog.V(2).Infof("Successfully updated status for %s", gpa.Name)
	return nil
}

// pathStatus actually does the patch request for the status of the given GPA
// do this because updateStatus is not supported by crd
func (a *GeneralController) pathStatus(gpa *autoscaling.GeneralPodAutoscaler, patch []byte) error {
	_, err := a.gpaNamespacer.GeneralPodAutoscalers(gpa.Namespace).Patch(gpa.Name, types.MergePatchType, patch)
	if err != nil {
		a.eventRecorder.Event(gpa, v1.EventTypeWarning, "FailedUpdateStatus", err.Error())
		return fmt.Errorf("failed to update status for %s: %v", gpa.Name, err)
	}
	klog.V(2).Infof("Successfully updated status for %s", gpa.Name)
	return nil
}

// setCondition sets the specific condition type on the given GPA to the specified value with the given reason
// and message.  The message and args are treated like a format string.  The condition will be added if it is
// not present.
func setCondition(gpa *autoscaling.GeneralPodAutoscaler, conditionType autoscaling.GeneralPodAutoscalerConditionType,
	status v1.ConditionStatus, reason, message string, args ...interface{}) {
	gpa.Status.Conditions = setConditionInList(gpa.Status.Conditions, conditionType, status, reason, message, args...)
}

// setConditionInList sets the specific condition type on the given GPA to the specified value with the given
// reason and message.  The message and args are treated like a format string.  The condition will be added if
// it is not present.  The new list will be returned.
func setConditionInList(inputList []autoscaling.GeneralPodAutoscalerCondition,
	conditionType autoscaling.GeneralPodAutoscalerConditionType, status v1.ConditionStatus, reason, message string,
	args ...interface{}) []autoscaling.GeneralPodAutoscalerCondition {
	resList := inputList
	var existingCond *autoscaling.GeneralPodAutoscalerCondition
	for i, condition := range resList {
		if condition.Type == conditionType {
			// can't take a pointer to an iteration variable
			existingCond = &resList[i]
			break
		}
	}

	if existingCond == nil {
		resList = append(resList, autoscaling.GeneralPodAutoscalerCondition{
			Type: conditionType,
		})
		existingCond = &resList[len(resList)-1]
	}

	if existingCond.Status != status {
		existingCond.LastTransitionTime = metav1.Now()
	}

	existingCond.Status = status
	existingCond.Reason = reason
	existingCond.Message = fmt.Sprintf(message, args...)

	return resList
}

func max(a, b int32) int32 {
	if a >= b {
		return a
	}
	return b
}

func min(a, b int32) int32 {
	if a <= b {
		return a
	}
	return b
}

func isEmpty(a autoscaling.AutoScalingDrivenMode) bool {
	return a.MetricMode == nil && a.EventMode == nil && a.TimeMode == nil && a.WebhookMode == nil && a.CronMetricMode == nil
}

func isComputeByLimits(gpa *autoscaling.GeneralPodAutoscaler) bool {
	computeByLimits := false
	if gpa != nil && gpa.Annotations != nil {
		computeByLimits = "true" == gpa.Annotations[computeByLimitsKey]
	}
	return computeByLimits
}
