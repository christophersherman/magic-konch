// Package workload resolves a pod to the durable controller that owns it,
// so Konch can key bash history by Deployment/StatefulSet/CronJob/etc. rather
// than by pod hash. The walk performs at most one extra GET per pod.
package workload

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Key identifies the workload that owns a pod.
//
// Kind is one of "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet",
// "CronJob", "Job", "Pod" (bare/unknown), or "Label" (only the
// app.kubernetes.io/name label was usable). Name is the resolved name to
// key history under.
type Key struct {
	Kind string
	Name string
}

// String renders the key as "Kind/Name", suitable for --probe output.
func (k Key) String() string { return k.Kind + "/" + k.Name }

// Resolver walks ownerReferences with a small per-session cache.
type Resolver struct {
	client kubernetes.Interface
	mu     sync.Mutex
	cache  map[string]Key
}

// New constructs a Resolver. The cache is per-Resolver, so one Resolver per
// CLI invocation is the intended usage.
func New(c kubernetes.Interface) *Resolver {
	return &Resolver{client: c, cache: map[string]Key{}}
}

// Resolve returns the workload key for pod, doing at most one extra GET
// (ReplicaSet→Deployment or Job→CronJob).
func (r *Resolver) Resolve(ctx context.Context, pod *corev1.Pod) (Key, error) {
	cacheKey := pod.Namespace + "/" + pod.Name
	r.mu.Lock()
	if k, ok := r.cache[cacheKey]; ok {
		r.mu.Unlock()
		return k, nil
	}
	r.mu.Unlock()
	k, err := r.resolve(ctx, pod)
	if err != nil {
		return Key{}, err
	}
	r.mu.Lock()
	r.cache[cacheKey] = k
	r.mu.Unlock()
	return k, nil
}

func (r *Resolver) resolve(ctx context.Context, pod *corev1.Pod) (Key, error) {
	owner := controllerOf(pod.OwnerReferences)
	if owner == nil {
		return labelOrPodFallback(pod), nil
	}
	switch owner.Kind {
	case "ReplicaSet":
		rs, err := r.client.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
				return Key{Kind: "ReplicaSet", Name: owner.Name}, nil
			}
			return Key{}, fmt.Errorf("get replicaset %s/%s: %w", pod.Namespace, owner.Name, err)
		}
		if dep := controllerOf(rs.OwnerReferences); dep != nil && dep.Kind == "Deployment" {
			return Key{Kind: "Deployment", Name: dep.Name}, nil
		}
		return Key{Kind: "ReplicaSet", Name: owner.Name}, nil
	case "Job":
		job, err := r.client.BatchV1().Jobs(pod.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
				return Key{Kind: "Job", Name: owner.Name}, nil
			}
			return Key{}, fmt.Errorf("get job %s/%s: %w", pod.Namespace, owner.Name, err)
		}
		if cj := controllerOf(job.OwnerReferences); cj != nil && cj.Kind == "CronJob" {
			return Key{Kind: "CronJob", Name: cj.Name}, nil
		}
		return Key{Kind: "Job", Name: owner.Name}, nil
	case "StatefulSet", "DaemonSet":
		return Key{Kind: owner.Kind, Name: owner.Name}, nil
	default:
		return labelOrPodFallback(pod), nil
	}
}

func controllerOf(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

func labelOrPodFallback(pod *corev1.Pod) Key {
	if v, ok := pod.Labels["app.kubernetes.io/name"]; ok && v != "" {
		return Key{Kind: "Label", Name: v}
	}
	return Key{Kind: "Pod", Name: pod.Name}
}
