// +build !ignore_autogenerated

/*
Copyright 2019 THL A29 Limited, a Tencent company.

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

// Code generated by deepcopy-gen. DO NOT EDIT.

package v1alpha1

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GPAControllerConfiguration) DeepCopyInto(out *GPAControllerConfiguration) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	out.GeneralPodAutoscalerSyncPeriod = in.GeneralPodAutoscalerSyncPeriod
	out.GeneralPodAutoscalerUpscaleForbiddenWindow = in.GeneralPodAutoscalerUpscaleForbiddenWindow
	out.GeneralPodAutoscalerDownscaleForbiddenWindow = in.GeneralPodAutoscalerDownscaleForbiddenWindow
	out.GeneralPodAutoscalerDownscaleStabilizationWindow = in.GeneralPodAutoscalerDownscaleStabilizationWindow
	out.GeneralPodAutoscalerCPUInitializationPeriod = in.GeneralPodAutoscalerCPUInitializationPeriod
	out.GeneralPodAutoscalerInitialReadinessDelay = in.GeneralPodAutoscalerInitialReadinessDelay
	return
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GPAControllerConfiguration.
func (in *GPAControllerConfiguration) DeepCopy() *GPAControllerConfiguration {
	if in == nil {
		return nil
	}
	out := new(GPAControllerConfiguration)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *GPAControllerConfiguration) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
