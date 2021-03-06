/*
Copyright 2018 Gravitational, Inc.

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

package report

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gravitational/gravity/lib/defaults"
	"github.com/gravitational/gravity/lib/utils"
	"github.com/gravitational/gravity/lib/utils/kubectl"

	log "github.com/sirupsen/logrus"
)

// NewKubernetesCollector returns a list of collectors to fetch kubernetes-specific
// diagnostics.
func NewKubernetesCollector(ctx context.Context, runner utils.CommandRunner, since time.Duration) Collectors {
	runner = planetContextRunner{runner}
	// collect general kubernetes info
	commands := Collectors{
		Cmd("k8s-nodes", utils.PlanetCommand(kubectl.Command("get", "nodes", "--output", "wide"))...),
		Cmd("k8s-describe-nodes", utils.PlanetCommand(kubectl.Command("describe", "nodes"))...),
	}
	for _, resourceType := range defaults.KubernetesReportResourceTypes {
		commands = append(commands,
			Cmd(fmt.Sprintf("k8s-describe-%s", resourceType),
				utils.PlanetCommand(kubectl.Command(
					"describe", resourceType, "--all-namespaces"))...),
			Cmd(fmt.Sprintf("k8s-%s", resourceType),
				utils.PlanetCommand(kubectl.Command(
					"get", resourceType, "--all-namespaces", "--output", "wide"))...),
		)
	}

	// collect previous container logs
	namespaces, err := kubectl.GetNamespaces(ctx, runner)
	if err != nil || len(namespaces) == 0 {
		namespaces = defaults.UsedNamespaces
	}
	commands = append(commands, capturePreviousContainerLogs(ctx, namespaces, runner, since)...)

	// collect current container logs
	if since == 0 {
		return append(commands, Cmd("k8s-cluster-info-dump.tgz", utils.Exe.Path, "system", "cluster-info"))
	}
	// kubectl cluster-info dump does not provide a --since flag, so collect
	// current container logs individually with kubectl logs.
	return append(commands, captureCurrentContainerLogs(ctx, namespaces, runner, since)...)
}

// capturePreviousContainerLogs collects logs for previously running container
// instances.
func capturePreviousContainerLogs(ctx context.Context, namespaces []string, runner utils.CommandRunner,
	since time.Duration) (collectors Collectors) {
	return captureContainerLogs(ctx, namespaces, runner, since, true)
}

// captureCurrentContainerLogs collects logs for currently running container
// instances.
func captureCurrentContainerLogs(ctx context.Context, namespaces []string, runner utils.CommandRunner,
	since time.Duration) (collectors Collectors) {
	return captureContainerLogs(ctx, namespaces, runner, since, false)
}

func captureContainerLogs(ctx context.Context, namespaces []string, runner utils.CommandRunner,
	since time.Duration, previous bool) (collectors Collectors) {
	for _, namespace := range namespaces {
		logger := log.WithField("namespace", namespace)
		pods, err := kubectl.GetPods(ctx, namespace, runner)
		if err != nil {
			logger.WithError(err).Warn("Failed to query pods.")
			continue
		}
		for _, pod := range pods {
			containers, err := kubectl.GetPodContainers(ctx, namespace, pod, runner)
			if err != nil {
				logger.WithFields(log.Fields{
					log.ErrorKey: err,
					"pod":        pod,
				}).Warn("Failed to query container.")
				continue
			}
			for _, container := range containers {
				collectors = append(collectors, containerLogCollector(namespace, pod, container, since, previous))
			}
		}
	}
	return collectors
}

// containerLogCollector returns a k8s log collector with the provided values
// and configuration. If previous is true, only collect logs for previously
// running instances of the container.
func containerLogCollector(namespace, pod, container string, since time.Duration, previous bool) (cmd Command) {
	name := fmt.Sprintf("k8s-logs-%v-%v-%v", namespace, pod, container)
	if previous {
		name += "-prev"
	}
	return Cmd(name, kubectl.Command("logs", pod,
		"--namespace", namespace,
		"--since", since.String(),
		fmt.Sprintf("-p=%v", strconv.FormatBool(previous)),
		fmt.Sprintf("-c=%v", container)).Args()...)
}

// RunStream executes the command specified with args in the context of the planet container
// Implements utils.CommandRunner
func (r planetContextRunner) RunStream(ctx context.Context, stdout, stderr io.Writer, args ...string) error {
	return r.CommandRunner.RunStream(ctx, stdout, stderr, utils.PlanetCommandSlice(args)...)
}

type planetContextRunner struct {
	utils.CommandRunner
}
