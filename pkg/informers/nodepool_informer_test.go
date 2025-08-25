package informers

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

func TestNewNodePoolInformer(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	collector := &metrics.Collector{}

	informer, err := NewNodePoolInformer(context.Background(), clientset, collector)
	if err != nil {
		t.Fatalf("Failed to create NodePool informer: %v", err)
	}

	if informer == nil {
		t.Fatal("Expected informer to be created, got nil")
	}

	if informer.collector != collector {
		t.Error("Expected collector to be set correctly")
	}

	if informer.nodepoolStates == nil {
		t.Error("Expected nodepoolStates map to be initialized")
	}

	if informer.informer == nil {
		t.Error("Expected informer to be initialized")
	}
}

func TestNodePoolInformerState(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	collector := &metrics.Collector{}

	informer, err := NewNodePoolInformer(context.Background(), clientset, collector)
	if err != nil {
		t.Fatalf("Failed to create NodePool informer: %v", err)
	}

	// Test initial state
	if len(informer.ListNodePools()) != 0 {
		t.Error("Expected initial state to have no NodePools")
	}

	// Test GetNodePoolState for non-existent NodePool
	state, exists := informer.GetNodePoolState("nonexistent")
	if exists {
		t.Error("Expected non-existent NodePool to return false")
	}
	if state != nil {
		t.Error("Expected non-existent NodePool to return nil state")
	}
}

func TestNodePoolGVK(t *testing.T) {
	expectedGroup := "karpenter.sh"
	expectedVersion := "v1"
	expectedKind := "NodePool"

	if NodePoolGVK.Group != expectedGroup {
		t.Errorf("Expected Group to be %s, got %s", expectedGroup, NodePoolGVK.Group)
	}

	if NodePoolGVK.Version != expectedVersion {
		t.Errorf("Expected Version to be %s, got %s", expectedVersion, NodePoolGVK.Version)
	}

	if NodePoolGVK.Kind != expectedKind {
		t.Errorf("Expected Kind to be %s, got %s", expectedKind, NodePoolGVK.Kind)
	}
}

func TestNodePoolState(t *testing.T) {
	state := &NodePoolState{
		Name:        "test-nodepool",
		APIVersion:  "karpenter.sh/v1",
		Kind:        "NodePool",
		Spec:        make(map[string]interface{}),
		Status:      make(map[string]interface{}),
		LastSeen:    time.Now(),
		ExpectedTTL: nil,
	}

	if state.Name != "test-nodepool" {
		t.Errorf("Expected Name to be 'test-nodepool', got %s", state.Name)
	}

	if state.APIVersion != "karpenter.sh/v1" {
		t.Errorf("Expected APIVersion to be 'karpenter.sh/v1', got %s", state.APIVersion)
	}

	if state.Kind != "NodePool" {
		t.Errorf("Expected Kind to be 'NodePool', got %s", state.Kind)
	}

	if state.Spec == nil {
		t.Error("Expected Spec to be initialized")
	}

	if state.Status == nil {
		t.Error("Expected Status to be initialized")
	}

	if state.ExpectedTTL != nil {
		t.Error("Expected ExpectedTTL to be nil initially")
	}
}
