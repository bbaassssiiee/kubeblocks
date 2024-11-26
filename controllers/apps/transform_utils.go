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

package apps

import (
	"context"
	"reflect"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	appsv1 "github.com/apecloud/kubeblocks/apis/apps/v1"
	dpv1alpha1 "github.com/apecloud/kubeblocks/apis/dataprotection/v1alpha1"
	workloads "github.com/apecloud/kubeblocks/apis/workloads/v1"
	"github.com/apecloud/kubeblocks/pkg/constant"
	intctrlutil "github.com/apecloud/kubeblocks/pkg/controllerutil"
	dptypes "github.com/apecloud/kubeblocks/pkg/dataprotection/types"
)

func newRequeueError(after time.Duration, reason string) error {
	return intctrlutil.NewRequeueError(after, reason)
}

func getGVKName(object client.Object, scheme *runtime.Scheme) (*gvkNObjKey, error) {
	gvk, err := apiutil.GVKForObject(object, scheme)
	if err != nil {
		return nil, err
	}
	return &gvkNObjKey{
		GroupVersionKind: gvk,
		ObjectKey: client.ObjectKey{
			Namespace: object.GetNamespace(),
			Name:      object.GetName(),
		},
	}, nil
}

func getAppInstanceML(cluster appsv1.Cluster) client.MatchingLabels {
	return client.MatchingLabels{
		constant.AppInstanceLabelKey: cluster.Name,
	}
}

func getFailedBackups(ctx context.Context,
	cli client.Reader,
	namespace string,
	labels client.MatchingLabels,
	owningNamespacedObjects owningObjects) error {
	backupList := &dpv1alpha1.BackupList{}
	if err := cli.List(ctx, backupList, client.InNamespace(namespace), labels); err != nil {
		return err
	}

	for i := range backupList.Items {
		backup := &backupList.Items[i]
		if backup.Status.Phase != dpv1alpha1.BackupPhaseFailed {
			continue
		}
		if backup.Labels[dptypes.BackupTypeLabelKey] != string(dpv1alpha1.BackupTypeContinuous) {
			gvr, err := getGVKName(backup, rscheme)
			if err != nil {
				return err
			}
			owningNamespacedObjects[*gvr] = backup
		}
	}
	return nil
}

func getOwningNamespacedObjects(ctx context.Context,
	cli client.Reader,
	namespace string,
	labels client.MatchingLabels,
	kinds []client.ObjectList) (owningObjects, error) {
	inNS := client.InNamespace(namespace)
	return getOwningObjectsWithOptions(ctx, cli, kinds, inNS, labels, inUniversalContext4C())
}

func getOwningNonNamespacedObjects(ctx context.Context,
	cli client.Reader,
	labels client.MatchingLabels,
	kinds []client.ObjectList) (owningObjects, error) {
	return getOwningObjectsWithOptions(ctx, cli, kinds, labels, inUniversalContext4C())
}

func getOwningObjectsWithOptions(ctx context.Context,
	cli client.Reader,
	kinds []client.ObjectList,
	opts ...client.ListOption) (owningObjects, error) {
	// list what kinds of object cluster owns
	objs := make(owningObjects)
	for _, list := range kinds {
		if err := cli.List(ctx, list, opts...); err != nil {
			return nil, err
		}
		// reflect get list.Items
		items := reflect.ValueOf(list).Elem().FieldByName("Items")
		l := items.Len()
		for i := 0; i < l; i++ {
			// get the underlying object
			object := items.Index(i).Addr().Interface().(client.Object)
			name, err := getGVKName(object, rscheme)
			if err != nil {
				return nil, err
			}
			objs[*name] = object
		}
	}
	return objs, nil
}

// sendWarningEventWithError sends a warning event when occurs error.
func sendWarningEventWithError(
	recorder record.EventRecorder,
	obj client.Object,
	reason string,
	err error) {
	// ignore requeue error
	if err == nil || intctrlutil.IsRequeueError(err) {
		return
	}
	controllerErr := intctrlutil.UnwrapControllerError(err)
	if controllerErr != nil {
		reason = string(controllerErr.Type)
	}
	recorder.Event(obj, corev1.EventTypeWarning, reason, err.Error())
}

func isVolumeResourceRequirementsEqual(a, b corev1.VolumeResourceRequirements) bool {
	return isResourceEqual(a.Requests, b.Requests) && isResourceEqual(a.Limits, b.Limits)
}

func isResourceRequirementsEqual(a, b corev1.ResourceRequirements) bool {
	return isResourceEqual(a.Requests, b.Requests) && isResourceEqual(a.Limits, b.Limits)
}

func isResourceEqual(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !v.Equal(b[k]) {
			return false
		}
	}
	return true
}

func isVolumeClaimTemplatesEqual(a, b []appsv1.ClusterComponentVolumeClaimTemplate) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		// first check resource requirements
		c := a[i].DeepCopy()
		d := b[i].DeepCopy()
		if !isVolumeResourceRequirementsEqual(c.Spec.Resources, d.Spec.Resources) {
			return false
		}

		// then clear resource requirements and check other fields
		c.Spec.Resources = corev1.VolumeResourceRequirements{}
		d.Spec.Resources = corev1.VolumeResourceRequirements{}
		if !reflect.DeepEqual(c, d) {
			return false
		}
	}
	return true
}

// isOwnedByComp is used to judge if the obj is owned by Component.
func isOwnedByComp(obj client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == appsv1.ComponentKind && ref.Controller != nil && *ref.Controller {
			return true
		}
	}
	return false
}

// isOwnedByInstanceSet is used to judge if the obj is owned by the InstanceSet controller
func isOwnedByInstanceSet(obj client.Object) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == workloads.InstanceSetKind && ref.Controller != nil && *ref.Controller {
			return true
		}
	}
	return false
}
