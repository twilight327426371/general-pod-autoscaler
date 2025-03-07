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
// limitations under the License.itations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/discovery"
	cacheddiscovery "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/scale"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/klog"
	resourceclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"
	"k8s.io/metrics/pkg/client/custom_metrics"
	"k8s.io/metrics/pkg/client/external_metrics"

	"github.com/ocgi/general-pod-autoscaler/cmd/gpa/app"
	"github.com/ocgi/general-pod-autoscaler/cmd/gpa/validator"
	autoscalingclient "github.com/ocgi/general-pod-autoscaler/pkg/client/clientset/versioned"
	autoscalinginformer "github.com/ocgi/general-pod-autoscaler/pkg/client/informers/externalversions"
	"github.com/ocgi/general-pod-autoscaler/pkg/metrics"
	"github.com/ocgi/general-pod-autoscaler/pkg/scaler"
	"github.com/ocgi/general-pod-autoscaler/pkg/version"
)

const (
	defaultLeaseDuration = 15 * time.Second
	defaultRenewDeadline = 10 * time.Second
	defaultRetryPeriod   = 2 * time.Second
)

func main() {
	runConfig := app.NewServerRunOptions()
	options := validator.NewServerRunOptions()
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()
	defer klog.Flush()
	version.Print()

	if options.ShowVersion {
		fmt.Println(os.Args[0], validator.Version)
		return
	}
	klog.Infof("Version: %s", validator.Version)

	klog.Infof("starting validator server.")
	if err := options.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	go func() {
		if err := validator.Run(options); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	}()
	leaderElection := defaultLeaderElectionConfiguration()
	if len(runConfig.ElectionResourceLock) != 0 {
		leaderElection.ResourceLock = runConfig.ElectionResourceLock
	}

	kubeconfig, err := runConfig.NewConfig()
	if err != nil {
		klog.Fatal("Failed to build config")
	}

	stop := server.SetupSignalHandler()

	client := kubernetes.NewForConfigOrDie(kubeconfig)

	gpaClient := autoscalingclient.NewForConfigOrDie(kubeconfig)

	coreFactory := informers.NewSharedInformerFactory(client, runConfig.Resync)
	scalerFactory := autoscalinginformer.NewSharedInformerFactory(gpaClient, runConfig.Resync)

	cachedClient := cacheddiscovery.NewMemCacheClient(discovery.NewDiscoveryClientForConfigOrDie(kubeconfig))
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(cachedClient)
	go wait.Until(func() {
		restMapper.Reset()
	}, 30*time.Second, stop)
	scaleKindResolver := scale.NewDiscoveryScaleKindResolver(client.Discovery())
	scaleClient, err := scale.NewForConfig(kubeconfig, restMapper, dynamic.LegacyAPIPathResolverFunc, scaleKindResolver)
	if err != nil {
		klog.Fatal("Failed to build scale client %v", err)
	}

	apiVersionsGetter := custom_metrics.NewAvailableAPIsGetter(gpaClient.Discovery())
	metricsClient := metrics.NewRESTMetricsClient(
		resourceclient.NewForConfigOrDie(kubeconfig),
		custom_metrics.NewForConfig(kubeconfig, restMapper, apiVersionsGetter),
		external_metrics.NewForConfigOrDie(kubeconfig),
	)

	controller := scaler.NewGeneralController(
		client.CoreV1(),
		scaleClient,
		gpaClient.AutoscalingV1alpha1(),
		restMapper,
		metricsClient,
		scalerFactory.Autoscaling().V1alpha1().GeneralPodAutoscalers(),
		coreFactory.Core().V1().Pods(),
		runConfig.GeneralPodAutoscalerSyncPeriod.Duration,
		runConfig.GeneralPodAutoscalerDownscaleStabilizationWindow.Duration,
		runConfig.GeneralPodAutoscalerTolerance,
		runConfig.GeneralPodAutoscalerCPUInitializationPeriod.Duration,
		runConfig.GeneralPodAutoscalerInitialReadinessDelay.Duration,
		runConfig.GeneralPodAutoscalerWorkers,
	)
	coreFactory.Start(stop)
	scalerFactory.Start(stop)
	ctx, cancel := context.WithCancel(context.TODO()) // TODO once Run() accepts a context, it should be used here
	defer cancel()

	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()

	run := func(ctx context.Context) {
		controller.Run(ctx.Done())
	}

	id, err := os.Hostname()
	if err != nil {
		klog.Fatalf("Unable to get hostname: %v", err)
	}

	lock, err := resourcelock.New(
		leaderElection.ResourceLock,
		runConfig.ElectionNamespace,
		runConfig.ElectionName,
		client.CoreV1(),
		client.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity: id,
		},
	)
	if err != nil {
		klog.Fatalf("Unable to create leader election lock: %v", err)
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: leaderElection.LeaseDuration.Duration,
		RenewDeadline: leaderElection.RenewDeadline.Duration,
		RetryPeriod:   leaderElection.RetryPeriod.Duration,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// Since we are committing a suicide after losing
				// mastership, we can safely ignore the argument.
				run(ctx)
			},
			OnStoppedLeading: func() {
				klog.Fatalf("lost master")
			},
		},
	})
}

func defaultLeaderElectionConfiguration() componentbaseconfig.LeaderElectionConfiguration {
	return componentbaseconfig.LeaderElectionConfiguration{
		LeaderElect:   false,
		LeaseDuration: metav1.Duration{Duration: defaultLeaseDuration},
		RenewDeadline: metav1.Duration{Duration: defaultRenewDeadline},
		RetryPeriod:   metav1.Duration{Duration: defaultRetryPeriod},
		ResourceLock:  resourcelock.LeasesResourceLock,
	}
}
