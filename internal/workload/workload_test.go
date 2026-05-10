package workload

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func ctlOwner(kind, name string) metav1.OwnerReference {
	t := true
	return metav1.OwnerReference{Kind: kind, Name: name, Controller: &t}
}

func TestResolve_DeploymentChain(t *testing.T) {
	ns := "apps"
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "csherman-net-7d9c",
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{ctlOwner("Deployment", "csherman-net")},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "csherman-net-7d9c-abcd",
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{ctlOwner("ReplicaSet", "csherman-net-7d9c")},
		},
	}
	cs := fake.NewSimpleClientset(rs)
	r := New(cs)

	got, err := r.Resolve(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if got != (Key{Kind: "Deployment", Name: "csherman-net"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_StandaloneReplicaSet(t *testing.T) {
	ns := "apps"
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "lonely-rs", Namespace: ns},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "lonely-rs-xyz",
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{ctlOwner("ReplicaSet", "lonely-rs")},
		},
	}
	cs := fake.NewSimpleClientset(rs)
	got, _ := New(cs).Resolve(context.Background(), pod)
	if got != (Key{Kind: "ReplicaSet", Name: "lonely-rs"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_StatefulSet(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "forgejo-0",
			Namespace:       "git",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("StatefulSet", "forgejo")},
		},
	}
	got, _ := New(fake.NewSimpleClientset()).Resolve(context.Background(), pod)
	if got != (Key{Kind: "StatefulSet", Name: "forgejo"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_DaemonSet(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "node-exporter-x",
			Namespace:       "monitoring",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("DaemonSet", "node-exporter")},
		},
	}
	got, _ := New(fake.NewSimpleClientset()).Resolve(context.Background(), pod)
	if got != (Key{Kind: "DaemonSet", Name: "node-exporter"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_CronJobChain(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "registry-gc-29900100",
			Namespace:       "registry",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("CronJob", "registry-gc")},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "registry-gc-29900100-abc",
			Namespace:       "registry",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("Job", "registry-gc-29900100")},
		},
	}
	got, _ := New(fake.NewSimpleClientset(job)).Resolve(context.Background(), pod)
	if got != (Key{Kind: "CronJob", Name: "registry-gc"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_StandaloneJob(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "j", Namespace: "x"}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "j-pod",
			Namespace:       "x",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("Job", "j")},
		},
	}
	got, _ := New(fake.NewSimpleClientset(job)).Resolve(context.Background(), pod)
	if got != (Key{Kind: "Job", Name: "j"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_LabelFallback(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ad-hoc-pod",
			Namespace: "default",
			Labels:    map[string]string{"app.kubernetes.io/name": "tools"},
		},
	}
	got, _ := New(fake.NewSimpleClientset()).Resolve(context.Background(), pod)
	if got != (Key{Kind: "Label", Name: "tools"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_PodNameLastResort(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "naked", Namespace: "default"},
	}
	got, _ := New(fake.NewSimpleClientset()).Resolve(context.Background(), pod)
	if got != (Key{Kind: "Pod", Name: "naked"}) {
		t.Errorf("got %+v", got)
	}
}

func TestResolve_CachesPerSession(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "x",
			Namespace:       "ns",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("Deployment", "dep")},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "x-pod",
			Namespace:       "ns",
			OwnerReferences: []metav1.OwnerReference{ctlOwner("ReplicaSet", "x")},
		},
	}
	cs := fake.NewSimpleClientset(rs)
	r := New(cs)

	if _, err := r.Resolve(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	// Second call should be cached — confirm by counting actions.
	priorActions := len(cs.Actions())
	if _, err := r.Resolve(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if len(cs.Actions()) != priorActions {
		t.Errorf("second Resolve should hit cache; saw new actions: %v", cs.Actions()[priorActions:])
	}
}
