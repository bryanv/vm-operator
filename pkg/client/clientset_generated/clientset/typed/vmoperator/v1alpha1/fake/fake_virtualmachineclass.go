/* **********************************************************
 * Copyright 2019 VMware, Inc.  All rights reserved. -- VMware Confidential
 * **********************************************************/

// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1alpha1 "github.com/vmware-tanzu/vm-operator/pkg/apis/vmoperator/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeVirtualMachineClasses implements VirtualMachineClassInterface
type FakeVirtualMachineClasses struct {
	Fake *FakeVmoperatorV1alpha1
	ns   string
}

var virtualmachineclassesResource = schema.GroupVersionResource{Group: "vmoperator.vmware.com", Version: "v1alpha1", Resource: "virtualmachineclasses"}

var virtualmachineclassesKind = schema.GroupVersionKind{Group: "vmoperator.vmware.com", Version: "v1alpha1", Kind: "VirtualMachineClass"}

// Get takes name of the virtualMachineClass, and returns the corresponding virtualMachineClass object, and an error if there is any.
func (c *FakeVirtualMachineClasses) Get(name string, options v1.GetOptions) (result *v1alpha1.VirtualMachineClass, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(virtualmachineclassesResource, c.ns, name), &v1alpha1.VirtualMachineClass{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.VirtualMachineClass), err
}

// List takes label and field selectors, and returns the list of VirtualMachineClasses that match those selectors.
func (c *FakeVirtualMachineClasses) List(opts v1.ListOptions) (result *v1alpha1.VirtualMachineClassList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(virtualmachineclassesResource, virtualmachineclassesKind, c.ns, opts), &v1alpha1.VirtualMachineClassList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.VirtualMachineClassList{ListMeta: obj.(*v1alpha1.VirtualMachineClassList).ListMeta}
	for _, item := range obj.(*v1alpha1.VirtualMachineClassList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested virtualMachineClasses.
func (c *FakeVirtualMachineClasses) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(virtualmachineclassesResource, c.ns, opts))

}

// Create takes the representation of a virtualMachineClass and creates it.  Returns the server's representation of the virtualMachineClass, and an error, if there is any.
func (c *FakeVirtualMachineClasses) Create(virtualMachineClass *v1alpha1.VirtualMachineClass) (result *v1alpha1.VirtualMachineClass, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(virtualmachineclassesResource, c.ns, virtualMachineClass), &v1alpha1.VirtualMachineClass{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.VirtualMachineClass), err
}

// Update takes the representation of a virtualMachineClass and updates it. Returns the server's representation of the virtualMachineClass, and an error, if there is any.
func (c *FakeVirtualMachineClasses) Update(virtualMachineClass *v1alpha1.VirtualMachineClass) (result *v1alpha1.VirtualMachineClass, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(virtualmachineclassesResource, c.ns, virtualMachineClass), &v1alpha1.VirtualMachineClass{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.VirtualMachineClass), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeVirtualMachineClasses) UpdateStatus(virtualMachineClass *v1alpha1.VirtualMachineClass) (*v1alpha1.VirtualMachineClass, error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceAction(virtualmachineclassesResource, "status", c.ns, virtualMachineClass), &v1alpha1.VirtualMachineClass{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.VirtualMachineClass), err
}

// Delete takes name of the virtualMachineClass and deletes it. Returns an error if one occurs.
func (c *FakeVirtualMachineClasses) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteAction(virtualmachineclassesResource, c.ns, name), &v1alpha1.VirtualMachineClass{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeVirtualMachineClasses) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(virtualmachineclassesResource, c.ns, listOptions)

	_, err := c.Fake.Invokes(action, &v1alpha1.VirtualMachineClassList{})
	return err
}

// Patch applies the patch and returns the patched virtualMachineClass.
func (c *FakeVirtualMachineClasses) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.VirtualMachineClass, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(virtualmachineclassesResource, c.ns, name, data, subresources...), &v1alpha1.VirtualMachineClass{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.VirtualMachineClass), err
}