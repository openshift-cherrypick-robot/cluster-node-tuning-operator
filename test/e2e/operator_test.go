// +build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
)

// Test the ClusterOperator node-tuning object exists and is Available.
func TestOperatorAvailable(t *testing.T) {
	cs := framework.NewClientSet()

	err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		co, err := cs.ClusterOperators().Get(tunedv1.TunedClusterOperatorResourceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		for _, cond := range co.Status.Conditions {
			if cond.Type == configv1.OperatorAvailable &&
				cond.Status == configv1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatalf("Did not get expected available condition: %v", err)
	}
}

// Test the default tuned CR exists.
func TestDefaultTunedExists(t *testing.T) {
	cs := framework.NewClientSet()

	err := wait.PollImmediate(1*time.Second, 5*time.Minute, func() (bool, error) {
		_, err := cs.Tuneds(ntoconfig.OperatorNamespace()).Get(tunedv1.TunedDefaultResourceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("Failed to get default tuned: %v", err)
	}
}

// Test the basic functionality of NTO and its operands.  The default sysctl(s)
// need(s) to be set across the nodes.
func TestWorkerNodeSysctl(t *testing.T) {
	sysctlVar := "net.ipv4.neigh.default.gc_thresh1"
	cs := framework.NewClientSet()

	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Ensuring the default worker node profile was set")
	err = ensureSysctl(sysctlVar, pod, "8192")
	if err != nil {
		t.Fatal(err)
	}
}

// Test the application (and rollback) of a custom profile via pod labelling.
func TestCustomProfileElasticSearch(t *testing.T) {
	const (
		profileElasticSearch  = "../../examples/elasticsearch.yaml"
		podLabelElasticSearch = "tuned.openshift.io/elasticsearch"
		sysctlVar             = "vm.max_map_count"
	)

	cs := framework.NewClientSet()

	t.Logf("Getting a list of worker nodes")
	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current value of %s in pod %s", sysctlVar, pod.Name)
	valOrig, err := getSysctl(sysctlVar, pod)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Labelling pod %s with label %s", pod.Name, podLabelElasticSearch)
	out, err := exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"=").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Applying the custom elasticsearch profile %s", profileElasticSearch)
	out, err = exec.Command("oc", "apply", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the custom worker node profile was set")
	err = ensureSysctl(sysctlVar, pod, "262144")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Removing label %s from pod %s", podLabelElasticSearch, pod.Name)
	out, err = exec.Command("oc", "label", "pod", "--overwrite", "-n", ntoconfig.OperatorNamespace(), pod.Name, podLabelElasticSearch+"-").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name)
	err = ensureSysctl(sysctlVar, pod, valOrig)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Deleting the custom elasticsearch profile %s", profileElasticSearch)
	out, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileElasticSearch).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}
}

// Test the application (and rollback) of a custom profile via node labelling.
func TestCustomProfileHugepages(t *testing.T) {
	const (
		profileHugepages   = "../../examples/hugepages.yaml"
		nodeLabelHugepages = "tuned.openshift.io/hugepages"
		sysctlVar          = "vm.nr_hugepages"
	)

	cs := framework.NewClientSet()

	t.Logf("Getting a list of worker nodes")
	nodes, err := getNodesByRole(cs, "worker")
	if err != nil {
		t.Fatal(err)
	}

	node := nodes[0]
	t.Logf("Getting a tuned pod running on node %s", node.Name)
	pod, err := getTunedForNode(cs, &node)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Getting the current value of %s in pod %s", sysctlVar, pod.Name)
	valOrig, err := getSysctl(sysctlVar, pod)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Labelling node %s with label %s", node.Name, nodeLabelHugepages)
	out, err := exec.Command("oc", "label", "node", "--overwrite", "-n", ntoconfig.OperatorNamespace(), node.Name, nodeLabelHugepages+"=").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Applying the custom hugepages profile %s", profileHugepages)
	out, err = exec.Command("oc", "apply", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the custom worker node profile was set")
	err = ensureSysctl(sysctlVar, pod, "16")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Deleting the custom hugepages profile %s", profileHugepages)
	out, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}

	t.Logf("Ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name)
	err = ensureSysctl(sysctlVar, pod, valOrig)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Removing label %s from node %s", nodeLabelHugepages, node.Name)
	out, err = exec.Command("oc", "label", "node", "--overwrite", "-n", ntoconfig.OperatorNamespace(), node.Name, nodeLabelHugepages+"-").CombinedOutput()
	if err != nil {
		t.Fatal(fmt.Errorf("%v", string(out)))
	}
}

// Returns a list of nodes that match a given role.
func getNodesByRole(cs *framework.ClientSet, role string) ([]corev1.Node, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{fmt.Sprintf("node-role.kubernetes.io/%s", role): ""}).String(),
	}
	nodeList, err := cs.Nodes().List(listOptions)
	if err != nil {
		return nil, fmt.Errorf("Couldn't get a list of nodes by role (%s): %v", role, err)
	}
	return nodeList.Items, nil
}

// Returns a pod that runs on a given node.
func getTunedForNode(cs *framework.ClientSet, node *corev1.Node) (*corev1.Pod, error) {
	listOptions := metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String(),
	}
	listOptions.LabelSelector = labels.SelectorFromSet(labels.Set{"openshift-app": "tuned"}).String()

	podList, err := cs.Pods(ntoconfig.OperatorNamespace()).List(listOptions)
	if err != nil {
		return nil, fmt.Errorf("Couldn't get a list of tuned pods: %v", err)
	}

	if len(podList.Items) != 1 {
		if len(podList.Items) == 0 {
			return nil, fmt.Errorf("Failed to find a tuned pod for node %s", node.Name)
		}
		return nil, fmt.Errorf("Too many (%d) tuned pods for node %s", len(podList.Items), node.Name)
	}
	return &podList.Items[0], nil
}

// Returns a sysctl value for sysctlVar from inside a (tuned) pod.
func getSysctl(sysctlVar string, pod *corev1.Pod) (val string, err error) {
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		var out []byte
		out, err = exec.Command("oc", "rsh", "-n", ntoconfig.OperatorNamespace(), pod.Name,
			"sysctl", "-n", sysctlVar).CombinedOutput()
		if err != nil {
			// Failed to query a sysctl "sysctlVar" on pod.Name
			return false, nil
		}
		val = strings.TrimSpace(string(out))
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("Failed to retrieve sysctl value %s in pod %s: %v", sysctlVar, pod.Name, err)
	}

	return val, nil
}

// Makes sure a sysctl value for sysctlVar from inside a (tuned) pod is equal to valExp.
// Returns an error otherwise.
func ensureSysctl(sysctlVar string, pod *corev1.Pod, valExp string) (err error) {
	var val string
	wait.PollImmediate(5*time.Second, 5*time.Minute, func() (bool, error) {
		val, err = getSysctl(sysctlVar, pod)
		if err != nil {
			return false, nil
		}

		if val != valExp {
			return false, nil
		}
		return true, nil
	})

	if val != valExp {
		return fmt.Errorf("sysctl %s=%s on %s, expected %s.", sysctlVar, val, pod.Name, valExp)
	}

	return nil
}
