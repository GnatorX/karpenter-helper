package watchers

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/garvinp/karpenter-helper/pkg/metrics"
)

type PodState struct {
	Pod           *corev1.Pod
	NominatedNode string
	CreationTime  time.Time
	ScheduledTime *time.Time
}

type PodWatcher struct {
	clientset *kubernetes.Clientset
	collector *metrics.Collector
	
	mu   sync.RWMutex
	pods map[string]*PodState // key: namespace/name
}

func NewPodWatcher(clientset *kubernetes.Clientset, collector *metrics.Collector) (*PodWatcher, error) {
	return &PodWatcher{
		clientset: clientset,
		collector: collector,
		pods:     make(map[string]*PodState),
	}, nil
}

func (pw *PodWatcher) Run(ctx context.Context) {
	klog.Info("Starting pod watcher")
	
	watchlist := &metav1.ListOptions{
		FieldSelector: fields.Everything().String(),
	}
	
	watcher, err := pw.clientset.CoreV1().Pods("").Watch(ctx, *watchlist)
	if err != nil {
		klog.Errorf("Failed to create pod watcher: %v", err)
		return
	}
	defer watcher.Stop()
	
	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Added:
			pw.handlePodAdded(event.Object.(*corev1.Pod))
		case watch.Modified:
			pw.handlePodModified(event.Object.(*corev1.Pod))
		case watch.Deleted:
			pw.handlePodDeleted(event.Object.(*corev1.Pod))
		}
	}
}

func (pw *PodWatcher) handlePodAdded(pod *corev1.Pod) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	
	podKey := pod.Namespace + "/" + pod.Name
	
	podState := &PodState{
		Pod:          pod,
		CreationTime: pod.CreationTimestamp.Time,
	}
	
	// Check if pod has a nominated node
	if nominatedNode := pw.getNominatedNode(pod); nominatedNode != "" {
		podState.NominatedNode = nominatedNode
		klog.V(2).Infof("Pod %s nominated for node %s", podKey, nominatedNode)
	}
	
	pw.pods[podKey] = podState
}

func (pw *PodWatcher) handlePodModified(pod *corev1.Pod) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	
	podKey := pod.Namespace + "/" + pod.Name
	
	podState, exists := pw.pods[podKey]
	if !exists {
		// Handle case where we missed the Add event
		pw.handlePodAdded(pod)
		return
	}
	
	oldPod := podState.Pod
	podState.Pod = pod
	
	// Check if pod just got scheduled
	if oldPod.Spec.NodeName == "" && pod.Spec.NodeName != "" {
		now := time.Now()
		podState.ScheduledTime = &now
		
		// Check if it was scheduled to its nominated node
		if podState.NominatedNode != "" {
			if pod.Spec.NodeName == podState.NominatedNode {
				klog.V(2).Infof("Pod %s successfully scheduled to nominated node %s", podKey, podState.NominatedNode)
			} else {
				// Pod landed on different node than nominated
				pw.collector.RecordNominatedPodFailure(pod.Name, pod.Namespace, podState.NominatedNode, "scheduled_elsewhere")
				klog.V(2).Infof("Pod %s nominated for node %s but scheduled to %s", podKey, podState.NominatedNode, pod.Spec.NodeName)
			}
		}
	}
	
	// Check for nomination changes
	if newNomination := pw.getNominatedNode(pod); newNomination != podState.NominatedNode {
		if podState.NominatedNode != "" && newNomination == "" {
			// Nomination was removed - might indicate failure
			if pod.Spec.NodeName == "" {
				pw.collector.RecordNominatedPodFailure(pod.Name, pod.Namespace, podState.NominatedNode, "nomination_removed")
			}
		}
		podState.NominatedNode = newNomination
	}
	
	// Check if pod failed after nomination
	if podState.NominatedNode != "" && pod.Status.Phase == corev1.PodFailed {
		pw.collector.RecordNominatedPodFailure(pod.Name, pod.Namespace, podState.NominatedNode, "pod_failed")
	}
}

func (pw *PodWatcher) handlePodDeleted(pod *corev1.Pod) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	
	podKey := pod.Namespace + "/" + pod.Name
	
	if podState, exists := pw.pods[podKey]; exists {
		// Check if pod was deleted before landing on nominated node
		if podState.NominatedNode != "" && pod.Spec.NodeName == "" {
			pw.collector.RecordNominatedPodFailure(pod.Name, pod.Namespace, podState.NominatedNode, "pod_deleted_before_schedule")
		}
		
		delete(pw.pods, podKey)
	}
}

func (pw *PodWatcher) getNominatedNode(pod *corev1.Pod) string {
	// Check for nominated node annotation
	if nominatedNode, exists := pod.Annotations[NominatedNodeAnnotation]; exists && nominatedNode != "" {
		return nominatedNode
	}
	
	// Check status.nominatedNodeName (newer K8s versions)
	if pod.Status.NominatedNodeName != "" {
		return pod.Status.NominatedNodeName
	}
	
	return ""
}

// CheckNominatedPodTimeouts checks for pods that have been nominated but haven't scheduled within expected time
func (pw *PodWatcher) CheckNominatedPodTimeouts() {
	pw.mu.RLock()
	defer pw.mu.RUnlock()
	
	now := time.Now()
	timeoutThreshold := 5 * time.Minute // configurable threshold
	
	for podKey, podState := range pw.pods {
		if podState.NominatedNode != "" && podState.Pod.Spec.NodeName == "" {
			// Pod is nominated but not scheduled
			if now.Sub(podState.CreationTime) > timeoutThreshold {
				pw.collector.RecordNominatedPodFailure(
					podState.Pod.Name,
					podState.Pod.Namespace,
					podState.NominatedNode,
					"nomination_timeout",
				)
				klog.V(2).Infof("Pod %s nomination timeout for node %s", podKey, podState.NominatedNode)
			}
		}
	}
}