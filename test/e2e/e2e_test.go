//go:build e2e

/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/lucasepe/the-reconciler-handbook/test/utils"
)

const (
	managerNamespace      = "reconciler-handbook-system"
	managerServiceAccount = "reconciler-handbook-controller-manager"
	managerDeployment     = "reconciler-handbook-controller-manager"
	metricsService        = "reconciler-handbook-controller-manager-metrics-service"
	metricsReaderRole     = "reconciler-handbook-metrics-reader"
	metricsBinding        = "reconciler-handbook-e2e-metrics-reader"
	leaderLease           = "648104d6.example.io"
	fixtureNamespace      = "reconciler-handbook-e2e-fixtures"
	curlImage             = "curlimages/curl:8.16.0@sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6"
	managerSelector       = "control-plane=controller-manager,app.kubernetes.io/name=reconciler-handbook"
)

var _ = Describe("complete-cluster evidence", Ordered, func() {
	BeforeAll(func() {
		By("creating and labeling the deployment namespace before any Pod is admitted")
		_, err := kubectl("create", "namespace", managerNamespace)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "namespace", managerNamespace,
			"pod-security.kubernetes.io/enforce=restricted",
			"pod-security.kubernetes.io/enforce-version=v1.37", "--overwrite")
		Expect(err).NotTo(HaveOccurred())

		By("installing the generated CRD and the test overlay")
		_, err = utils.Run(exec.Command("make", "install"))
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("apply", "-k", "config/e2e")
		Expect(err).NotTo(HaveOccurred())

		By("waiting for Kubernetes to report the manager Deployment available")
		_, err = kubectl("rollout", "status", "deployment/"+managerDeployment,
			"-n", managerNamespace, "--timeout=3m")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		By("removing the cluster-scoped test metrics binding")
		_, _ = kubectl("delete", "clusterrolebinding", metricsBinding, "--ignore-not-found")
		By("removing fixtures and the deployed test overlay")
		_, _ = kubectl("delete", "namespace", fixtureNamespace, "--ignore-not-found", "--wait=true", "--timeout=2m")
		_, _ = kubectl("delete", "-k", "config/e2e", "--ignore-not-found", "--wait=true", "--timeout=2m")
	})

	AfterEach(func() {
		if !CurrentSpecReport().Failed() {
			return
		}
		By("collecting credential-free cluster diagnostics")
		for _, args := range [][]string{
			{"get", "deployment,pods,serviceaccounts", "-n", managerNamespace, "-o", "wide"},
			{"get", "lease", leaderLease, "-n", managerNamespace, "-o", "yaml"},
			{"get", "events", "-n", managerNamespace, "--sort-by=.lastTimestamp"},
		} {
			if output, err := kubectl(args...); err == nil {
				_, _ = fmt.Fprintln(GinkgoWriter, output)
			} else {
				_, _ = fmt.Fprintln(GinkgoWriter, err)
			}
		}
	})

	It("runs the selected manager image under the restricted profile and becomes ready", func() {
		podName := oneManagerPod()

		phase, err := kubectl("get", "pod", podName, "-n", managerNamespace, "-o", "jsonpath={.status.phase}")
		Expect(err).NotTo(HaveOccurred())
		Expect(phase).To(Equal("Running"))
		ready, err := kubectl("get", "pod", podName, "-n", managerNamespace,
			"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(Equal("True"))

		security, err := kubectl("get", "deployment", managerDeployment, "-n", managerNamespace,
			"-o", "jsonpath={.spec.template.spec.securityContext.runAsNonRoot}{'|'}{.spec.template.spec.securityContext.seccompProfile.type}{'|'}{.spec.template.spec.containers[0].securityContext.allowPrivilegeEscalation}{'|'}{.spec.template.spec.containers[0].securityContext.readOnlyRootFilesystem}{'|'}{.spec.template.spec.containers[0].securityContext.capabilities.drop[0]}")
		Expect(err).NotTo(HaveOccurred())
		Expect(security).To(Equal("true|RuntimeDefault|false|true|ALL"))

		image, err := kubectl("get", "deployment", managerDeployment, "-n", managerNamespace,
			"-o", "jsonpath={.spec.template.spec.containers[0].image}")
		Expect(err).NotTo(HaveOccurred())
		Expect(image).To(Equal(managerImage))
		imageID, err := kubectl("get", "pod", podName, "-n", managerNamespace,
			"-o", "jsonpath={.status.containerStatuses[0].imageID}")
		Expect(err).NotTo(HaveOccurred())
		Expect(imageID).To(ContainSubstring("sha256:"))
	})

	It("exercises the manager service account's installed authorization surface", func() {
		identity := "--as=system:serviceaccount:" + managerNamespace + ":" + managerServiceAccount
		checks := []struct {
			want string
			args []string
		}{
			{"yes", []string{"auth", "can-i", "get", "leases.coordination.k8s.io", "-n", managerNamespace, identity}},
			{"yes", []string{"auth", "can-i", "update", "leases.coordination.k8s.io", "-n", managerNamespace, identity}},
			{"no", []string{"auth", "can-i", "create", "secrets", "-n", managerNamespace, identity}},
			{"no", []string{"auth", "can-i", "update", "manageddatabases.data.example.io", "-n", managerNamespace, identity}},
		}
		for _, check := range checks {
			output, err := kubectl(check.args...)
			if check.want == "yes" {
				Expect(err).NotTo(HaveOccurred())
			} else {
				// kubectl deliberately exits non-zero when the authorizer answers no.
				Expect(err).To(HaveOccurred())
			}
			Expect(strings.TrimSpace(output)).To(Equal(check.want), strings.Join(check.args, " "))
		}
	})

	It("denies one authenticated scraper and authorizes only the bound scraper", func() {
		manifest := fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: metrics-denied
  namespace: %[1]s
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: metrics-allowed
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: %[2]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: %[3]s
subjects:
- kind: ServiceAccount
  name: metrics-allowed
  namespace: %[1]s
`, managerNamespace, metricsBinding, metricsReaderRole)
		_, err := kubectlInput(manifest, "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())

		createMetricsProbe("metrics-denied", "metrics-denied", "403")
		createMetricsProbe("metrics-allowed", "metrics-allowed", "200")
		Expect(podLog("metrics-denied")).To(Equal("403"))
		Expect(podLog("metrics-allowed")).To(Equal("200"))
	})

	It("uses the installed CRD for admission and stored defaulting", func() {
		_, err := kubectl("create", "namespace", fixtureNamespace)
		Expect(err).NotTo(HaveOccurred())

		valid := fmt.Sprintf(`
apiVersion: data.example.io/v1alpha1
kind: ManagedDatabase
metadata:
  name: admitted
  namespace: %s
spec:
  engine: postgres
  size: small
  region: eu-west
`, fixtureNamespace)
		_, err = kubectlInput(valid, "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())
		storedDefault, err := kubectl("get", "manageddatabase", "admitted", "-n", fixtureNamespace,
			"-o", "jsonpath={.spec.deletionPolicy}")
		Expect(err).NotTo(HaveOccurred())
		Expect(storedDefault).To(Equal("Retain"))

		invalid := fmt.Sprintf(`
apiVersion: data.example.io/v1alpha1
kind: ManagedDatabase
metadata:
  name: rejected
  namespace: %s
spec:
  engine: mysql
  size: small
  region: eu-west
`, fixtureNamespace)
		_, err = kubectlInput(invalid, "apply", "-f", "-")
		Expect(err).To(HaveOccurred())
		_, err = kubectl("get", "manageddatabase", "rejected", "-n", fixtureNamespace)
		Expect(err).To(HaveOccurred())
	})

	It("observes garbage collection through an exact same-namespace owner reference", func() {
		owner := fmt.Sprintf(`
apiVersion: data.example.io/v1alpha1
kind: ManagedDatabase
metadata:
  name: garbage-collected
  namespace: %s
spec:
  engine: postgres
  size: small
  region: eu-west
  deletionPolicy: Retain
`, fixtureNamespace)
		_, err := kubectlInput(owner, "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())
		uid, err := kubectl("get", "manageddatabase", "garbage-collected", "-n", fixtureNamespace,
			"-o", "jsonpath={.metadata.uid}")
		Expect(err).NotTo(HaveOccurred())
		Expect(uid).NotTo(BeEmpty())

		secret := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: garbage-collected-connection
  namespace: %[1]s
  ownerReferences:
  - apiVersion: data.example.io/v1alpha1
    kind: ManagedDatabase
    name: garbage-collected
    uid: %[2]s
    controller: true
    blockOwnerDeletion: true
type: Opaque
`, fixtureNamespace, uid)
		_, err = kubectlInput(secret, "apply", "-f", "-")
		Expect(err).NotTo(HaveOccurred())

		_, err = kubectl("delete", "manageddatabase", "garbage-collected", "-n", fixtureNamespace,
			"--wait=true", "--timeout=90s")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) string {
			output, err := kubectl("get", "secret", "garbage-collected-connection", "-n", fixtureNamespace,
				"--ignore-not-found", "-o", "name")
			g.Expect(err).NotTo(HaveOccurred())
			return strings.TrimSpace(output)
		}, 90*time.Second, time.Second).Should(BeEmpty())
	})

	It("observes Lease acquisition and handoff between two live manager Pods", func() {
		_, err := kubectl("scale", "deployment", managerDeployment, "-n", managerNamespace, "--replicas=2")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("rollout", "status", "deployment/"+managerDeployment,
			"-n", managerNamespace, "--timeout=3m")
		Expect(err).NotTo(HaveOccurred())
		Eventually(managerPodNames, 2*time.Minute, time.Second).Should(HaveLen(2))

		oldHolder := leaseField("{.spec.holderIdentity}")
		oldTransitions := leaseIntField("{.spec.leaseTransitions}")
		oldResourceVersion := leaseField("{.metadata.resourceVersion}")
		oldPod := holderPod(oldHolder)
		Expect(managerPodNames()).To(ContainElement(oldPod))

		_, err = kubectl("delete", "pod", oldPod, "-n", managerNamespace, "--wait=false")
		Expect(err).NotTo(HaveOccurred())

		var newHolder string
		Eventually(func(g Gomega) {
			newHolder = leaseField("{.spec.holderIdentity}")
			g.Expect(newHolder).NotTo(Equal(oldHolder))
			g.Expect(leaseField("{.metadata.resourceVersion}")).NotTo(Equal(oldResourceVersion))
			g.Expect(leaseIntField("{.spec.leaseTransitions}")).To(BeNumerically(">", oldTransitions))
			g.Expect(managerPodNames()).To(ContainElement(holderPod(newHolder)))
		}, 2*time.Minute, time.Second).Should(Succeed())

		_, err = kubectl("rollout", "status", "deployment/"+managerDeployment,
			"-n", managerNamespace, "--timeout=3m")
		Expect(err).NotTo(HaveOccurred())
		Eventually(managerPodNames, 2*time.Minute, time.Second).Should(HaveLen(2))
		Expect(holderPod(newHolder)).NotTo(Equal(oldPod))
	})
})

func kubectl(args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl", args...))
}

func kubectlInput(input string, args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(input)
	return utils.Run(cmd)
}

func oneManagerPod() string {
	Eventually(managerPodNames, 2*time.Minute, time.Second).Should(HaveLen(1))
	return managerPodNames()[0]
}

func managerPodNames() []string {
	output, err := kubectl("get", "pods", "-n", managerNamespace, "-l", managerSelector,
		"-o", "json")
	if err != nil {
		return nil
	}
	var pods struct {
		Items []struct {
			Metadata struct {
				Name              string  `json:"name"`
				DeletionTimestamp *string `json:"deletionTimestamp"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &pods); err != nil {
		return nil
	}
	var names []string
	for _, pod := range pods.Items {
		if pod.Metadata.DeletionTimestamp == nil && pod.Status.Phase == "Running" {
			names = append(names, pod.Metadata.Name)
		}
	}
	return names
}

func createMetricsProbe(name, serviceAccount, expectedStatus string) {
	manifest := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  restartPolicy: Never
  serviceAccountName: %[3]s
  automountServiceAccountToken: true
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: probe
    image: %[4]s
    imagePullPolicy: IfNotPresent
    command: ["/bin/sh", "-c"]
    args:
    - |
      status="$(curl --silent --show-error --insecure --output /dev/null --write-out '%%{http_code}' --header "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" https://%[5]s.%[2]s.svc.cluster.local:8443/metrics)"
      printf '%%s\n' "$status"
      test "$status" = "%[6]s"
    securityContext:
      runAsUser: 1000
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
`, name, managerNamespace, serviceAccount, curlImage, metricsService, expectedStatus)
	_, err := kubectlInput(manifest, "apply", "-f", "-")
	Expect(err).NotTo(HaveOccurred())
	_, err = kubectl("wait", "pod/"+name, "-n", managerNamespace,
		"--for=jsonpath={.status.phase}=Succeeded", "--timeout=2m")
	Expect(err).NotTo(HaveOccurred())
}

func podLog(name string) string {
	output, err := kubectl("logs", name, "-n", managerNamespace)
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(output)
}

func leaseField(path string) string {
	var output string
	Eventually(func(g Gomega) {
		var err error
		output, err = kubectl("get", "lease", leaderLease, "-n", managerNamespace, "-o", "jsonpath="+path)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(output)).NotTo(BeEmpty())
	}, 90*time.Second, time.Second).Should(Succeed())
	return strings.TrimSpace(output)
}

func leaseIntField(path string) int64 {
	raw := leaseField(path)
	value, err := strconv.ParseInt(raw, 10, 64)
	Expect(err).NotTo(HaveOccurred())
	return value
}

func holderPod(identity string) string {
	parts := strings.SplitN(identity, "_", 2)
	Expect(parts).To(HaveLen(2), "controller-runtime holder identity must be hostname_UUID")
	Expect(parts[1]).NotTo(BeEmpty())
	return parts[0]
}
