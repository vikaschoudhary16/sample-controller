/*
Copyright 2017 The Kubernetes Authors.

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

package main

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"k8s.io/sample-controller/metrics"
	samplev1alpha1 "k8s.io/sample-controller/pkg/apis/samplecontroller/v1alpha1"
	clientset "k8s.io/sample-controller/pkg/generated/clientset/versioned"
	samplescheme "k8s.io/sample-controller/pkg/generated/clientset/versioned/scheme"
	informers "k8s.io/sample-controller/pkg/generated/informers/externalversions/samplecontroller/v1alpha1"
	listers "k8s.io/sample-controller/pkg/generated/listers/samplecontroller/v1alpha1"
	"k8s.io/sample-controller/pkg/util"

	cloudsvcapi "github.com/abccloud/abccloud-go-sdk/service/instances"
)

const controllerAgentName = "sample-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a VM is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a VM fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by VM"
	// MessageResourceSynced is the message used for an Event fired when a VM
	// is synced successfully
	MessageResourceSynced = "VM synced successfully"

	AddEvent    = "ADD"
	DeleteEvent = "DEL"
)

var UnlinkedInstances map[string]time.Time
var LinkedInstances []string

// Controller is the controller implementation for VM resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	sampleclientset clientset.Interface

	deploymentsLister appslisters.DeploymentLister
	deploymentsSynced cache.InformerSynced
	vmsLister         listers.VMLister
	vmsSynced         cache.InformerSynced
	vmSvc             cloudsvcapi.VMService

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	sampleclientset clientset.Interface,
	deploymentInformer appsinformers.DeploymentInformer,
	vmInformer informers.VMInformer,
	vmCloudService cloudsvcapi.VMService) *Controller {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(samplescheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:   kubeclientset,
		sampleclientset: sampleclientset,
		vmsLister:       vmInformer.Lister(),
		vmsSynced:       vmInformer.Informer().HasSynced,
		vmSvc:           vmCloudService,
		workqueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "VMs"),
		recorder:        recorder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when VM resources change
	vmInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.handleAddObject,
		DeleteFunc: controller.handleDeleteObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting VM controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.vmsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process VM resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// VM resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func splitKey(key string) (namespace, name, event string, err error) {
	parts := strings.split(key, "/")
	switch len(parts) {
	case 3:
		// namespace, name and event
		return parts[0], parts[1], parts[2], nil
	}

	return "", "", "", fmt.errorf("unexpected key format: %q", key)
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the VM resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// convert the namespace/name string into a distinct namespace and name
	namespace, name, event, err := splitKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	switch event {
	case "ADD":
		return syncAddHandler(namespace, name)
	case "DELETE":
		return syncDeleteHandler(namespace, name)
	}

	utilruntime.HandleError(fmt.errorf("Event %s from Key %s is unexpected and being ignored ", event, key))
	return nil
}

func (c *Controller) syncAddHandler(namespace string, name string) error {
	// Get the VM resource with this namespace/name
	vm, err := c.vmsLister.VMs(namespace).Get(name)
	if err != nil {
		// The VM resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("vm '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}

	// If object hasn't been deleted and doesn't have a finalizer, add one
	// Add a finalizer to newly created objects. This is to hold deletion of VM resource until cloud instance gets deleted
	if !util.Contains(vm.ObjectMeta.Finalizers, samplev1alpha1.VMFinalizer) {
		vm.Finalizers = append(vm.Finalizers, samplev1alpha1.VMFinalizer)
		if _, err := c.sampleclientset.SamplecontrollerV1alpha1().VMs(vm.Namespace).Update(vm); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to add finalizer to VM object %v due to error %v.", name, err))
			return err
		}
	}

	vmName := vm.Spec.VMName
	suuid := vm.Status.VMId
	if vmName == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again.
		utilruntime.HandleError(fmt.Errorf("%s: vm name must be specified", key))
		return nil
	}
	if suuid != "" {
		// Get the cloud instance with the uuid specified in VM.Status
		resp := c.vmSvc.Get(suuid)
		if resp.StatusCode == cloudsvcapi.STATUS_CODE_SUCCESS {
			return nil
		}
	}
	// check name availability
	if !c.vmSvc.NameAvailable() {
		utilruntime.HandleError(fmt.Errorf("%s: vm name, %s,  is not allowed. Ignoring event!!!", key, vmName))
		return nil
	}

	// create cloud vm instance
	createStartTime := time.Now()
	resp := c.vmSvc.Create(cloudsvcapi.CreateRequest{Name: vmName})
	metrics.UpdateDurationFromStart(metrics.Create, createStartTime)
	if resp.StatusCode != cloudsvcapi.STATUS_CODE_CREATE_SUCCESS {
		if resp.StatusCode == cloudsvcapi.STATUS_CODE_CREATE_FAILURE_NAME_DUPLICATION {
			// There is no chance that this error is going to fix on retry, so just log error and do not retry
			utilruntime.HandleError(fmt.Errorf("%s: cloud instance create failed!!! vm with name %s already exists!!!", key, vmName))
			metrics.RegisterFailedCreate(metrics.DuplicateName)
			return nil

		}
		metrics.RegisterFailedCreate(metrics.NotSure)
		// In any other failure lets keep on retrying until max number of time
		return fmt.Errorf("%s: cloud instance create failed, Retrying !!!", key)
	}

	// Finally, we update the status block of the VM resource to reflect the
	// current state of the world
	err = c.updateVMStatus(vm, resp.Uuid)
	if err != nil {
		return err
	}

	c.recorder.Event(vm, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) syncDeleteHandler(namespace string, name string) error {
	// Get the VM resource with this namespace/name
	vm, err := c.vmsLister.VMs(namespace).Get(name)
	if err != nil {
		// The VM resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("vm '%s' in work queue no longer exists", key))
			return nil
		}
		return err
	}
	if vm.Status.VMId == "" {
		//remove Finalizer
		vm.ObjectMeta.Finalizers = util.Filter(vm.ObjectMeta.Finalizers, samplev1alpha1.VMFinalizer)
		if _, err := c.sampleclientset.SamplecontrollerV1alpha1().VMs(vm.Namespace).Update(vm); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to remove finalizer from VM object %v due to error %v.", name, err))
			return err
		}

		utilruntime.HandleError(fmt.Errorf("vm '%s' has no reference to any cloud instance. Removing finalizer so that its deletion can progress", key))
		return nil
	}
	deleteStartTime := time.Now()
	resp := c.vmSvc.Delete(cloudsvcapi.DeleteRequest{Uuid: vm.Status.VMId})
	metrics.UpdateDurationFromStart(metrics.Delete, deleteStartTime)
	if resp.StatusCode == cloudsvcapi.STATUS_CODE_DELETE_SUCCESS {
		//remove Finalizer
		vm.ObjectMeta.Finalizers = util.Filter(vm.ObjectMeta.Finalizers, samplev1alpha1.VMFinalizer)
		if _, err := c.sampleclientset.SamplecontrollerV1alpha1().VMs(vm.Namespace).Update(vm); err != nil {
			utilruntime.HandleError(fmt.Errorf("failed to remove finalizer from VM object %v due to error %v.", name, err))
			metrics.RegisterError(fmt.Errorf("failed to remove finalizer from VM object %v due to error %v.", name, err))
			return err
		}
		return nil
	}
	return fmt.Errorf("Failed to delete cloud instance for vm %s. STATUS CODE %v", name, resp.StatusCode)
}

func (c *Controller) updateVMStatus(vm *samplev1alpha1.VM, uuid string) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	vmCopy := vm.DeepCopy()
	vmCopy.Status.VMId = uuid
	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the VM resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	_, err := c.sampleclientset.SamplecontrollerV1alpha1().VMs(vm.Namespace).Update(vmCopy)
	return err
}

// enqueueVM takes a VM resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than VM.
func (c *Controller) enqueueVM(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key + "/" + event)
}

func (c *Controller) handleAddObject(obj interface{}) {
	c.enqueueVM(c.handleObject(obj), "ADD")
}

func (c *Controller) handleDeleteObject(obj interface{}) {
	c.enqueueVM(c.handleObject(obj), "DELETE")
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the VM resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that VM resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) *v1alpha1.VM {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a VM, we should not do anything more
		// with it.
		if ownerRef.Kind != "VM" {
			return
		}

		vm, err := c.vmsLister.VMs(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			klog.V(4).Infof("ignoring orphaned object '%s' of vm '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}
		return vm
	}
}

func (c *Controller) CollectGarbageAndUpdateCPUForever(stopCh <-chan struct{}) {
	for {
		select {
		case <-time.After(*time.Second * 5):
			{
				metrics.UpdateLastTimeGC(metrics.GarbageCollect, loopStart)
				loopStart := time.Now()
				runOnce(loopStart)
			}
		case <-stopCh:
			return
		}
	}
}

func (c *Controller) runOnce(currentTime time.Time) {
	// 1. Get VM list
	// 2. Get "AllInstance" list
	// 3. for each VM
	//     3.1 Get instance for id in VM status
	//     3.2 get Instance status
	//     3.3 update CPU in VM status
	//     3.4 Add instance to "LinkedInstance" list
	// 4. Take "AllInstance" - "LinkedInstance"
	// 5. Add each to "UnlinkedInstances" with timestamp of each first observation
	// 6. While adding check if Instance is already there and if it has been there long enough, then delete the instance and remove it from the unlinkedlist
	//
	vms, err := c.vmsLister.List()
	if err != nil {
		klog.Errorf("Error in list VM objects at k8s: %v", err)
		// TODO update metric
		return
	}
	resp := c.vmSvc.List()
	if resp.StatusCode == cloudsvcapi.STATUS_CODE_FAILURE {
		klog.Errorf("Error in listing instances at cloud: %v", resp.StatusCode)
		metrics.RegisterError(fmt.Errorf("Error in listing instances at cloud: %v", resp.StatusCode))
		return
	}
	for _, vm := range vms {
		for _, instance := range resp.Instances {
			if instance.Uuid == vm.Status.VMId {
				iStatus := cloudsvcapi.GetStatus(instance.Uuid)
				if iStatus.StatusCode != cloudsvcapi.STATUS_CODE_SUCCESS {
					klog.Errorf("Error in getting instance status for vm %s: %v", vm.Name, iStatus.StatusCode)
					metrics.RegisterError(fmt.Errorf("Error in getting instance status for vm %s: %v", vm.Name, iStatus.StatusCode))
				}
				vm.Status.CPUUtilization = iStatus.CPUUtilization
				append(LinkedInstances, instance.Uuid)
			}
		}
	}
	for _, instance := range resp.Instances {
		isInstanceLinked := false
		for _, linkedInstance := range LinkedInstances {
			if linkedInstance == instance.Uuid {
				isInstanceLinked = true
				break
			}
		}
		if !isInstanceLinked {
			if firstObservedTime, ok := UnlinkedInstances[instance.Uuid]; ok {
				if firstObservedTime.Add(TimeToWaitBeforeDeletingUnlinkedInstance).After(currentTime) {
					resp := cloudsvcapi.Delete(cloudsvcapi.DeleteRequest{Uuid: instance.Uuid})
					if resp.StatusCode != cloudsvcapi.STATUS_CODE_DELETE_SUCCESS {
						klog.Errorf("Error in deleting instance %s : %v", instance.Name, resp.StatusCode)
						metrics.RegisterError(fmt.Errorf("Error in deleting instance %s : %v", instance.Name, resp.StatusCode))
					} else {
						delete(UnlinkedInstances, instance.Uuid)
					}
				}
			} else {
				UnlinkedInstances[instance.Uuid] = currentTime
			}
		}
	}
}
