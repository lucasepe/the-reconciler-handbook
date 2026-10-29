//go:build e2e

/*
Copyright (c) 2026 Luca Sepe

SPDX-License-Identifier: MIT
See the LICENSE file in the project root for the full license text.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/lucasepe/the-reconciler-handbook/test/utils"
)

const managerImage = "reconciler-handbook-manager:production-evidence"

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "complete-cluster evidence suite")
}

var _ = BeforeSuite(func() {
	Expect(os.Getenv("KUBECONFIG")).NotTo(BeEmpty(), "the e2e run requires its dedicated kubeconfig")
	Expect(os.Getenv("KIND_CLUSTER")).NotTo(BeEmpty(), "the e2e run requires its owned Kind cluster name")

	By("building the manager image used by the disposable cluster")
	_, err := utils.Run(exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage)))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	By("recording the immutable local manager image identity")
	imageID, err := utils.Run(exec.Command("docker", "image", "inspect", managerImage, "--format={{.Id}}"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, strings.TrimSpace(imageID)).To(MatchRegexp(`^sha256:[a-f0-9]{64}$`))
	_, _ = fmt.Fprintf(GinkgoWriter, "manager image: %s (%s)\n", managerImage, strings.TrimSpace(imageID))

	By("loading that image into the owned Kind cluster")
	ExpectWithOffset(1, utils.LoadImageToKindClusterWithName(managerImage)).To(Succeed())

	// kuberc is user-level state and is outside this isolated experiment.
	Expect(os.Setenv("KUBECTL_KUBERC", "false")).To(Succeed())
})
