/*
Copyright (C) 2022-2024 ApeCloud Co., Ltd

This file is part of KubeBlocks project

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package util

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	ctlruntime "sigs.k8s.io/controller-runtime"

	"github.com/apecloud/kubeblocks/pkg/constant"
)

const (
	sendEventMaxAttempts   = 30
	sendEventRetryInterval = 10 * time.Second
)

func SendEventWithMessage(logger *logr.Logger, reason string, message string) {
	go func() {
		event := createEvent(reason, message)
		err := sendEvent(event)
		if logger != nil && err != nil {
			logger.Error(err, "send event failed")
		}
	}()
}

func createEvent(reason string, message string) *corev1.Event {
	// TODO(v1.0): pod variables
	podName := os.Getenv(constant.KBEnvPodName)
	podUID := os.Getenv(constant.KBEnvPodUID)
	nodeName := os.Getenv(constant.KBEnvNodeName)
	namespace := os.Getenv(constant.KBEnvNamespace)
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s.%s", podName, rand.String(16)),
			Namespace: namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Namespace: namespace,
			Name:      podName,
			UID:       types.UID(podUID),
			FieldPath: "spec.containers{kbagent}",
		},
		Reason:  reason,
		Message: message,
		Source: corev1.EventSource{
			Component: "kbagent",
			Host:      nodeName,
		},
		FirstTimestamp:      metav1.Now(),
		LastTimestamp:       metav1.Now(),
		EventTime:           metav1.NowMicro(),
		ReportingController: "kbagent",
		ReportingInstance:   podName,
		Action:              reason,
		Type:                "Normal",
	}
}

func sendEvent(event *corev1.Event) error {
	clientSet, err := getK8sClientSet()
	if err != nil {
		return err
	}
	namespace := os.Getenv(constant.KBEnvNamespace)
	for i := 0; i < sendEventMaxAttempts; i++ {
		_, err = clientSet.CoreV1().Events(namespace).Create(context.Background(), event, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		time.Sleep(sendEventRetryInterval)
	}
	return err
}

func getK8sClientSet() (*kubernetes.Clientset, error) {
	restConfig, err := ctlruntime.GetConfig()
	if err != nil {
		return nil, errors.Wrap(err, "get kubeConfig failed")
	}
	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return clientSet, nil
}