//go:build e2e
// +build e2e

/*
Copyright 2026 Raj Singh.

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

package e2e

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rajsinghtech/garage-operator/test/utils"
)

var _ = Describe("WebAPI", Ordered, func() {
	const (
		clusterName       = "garage-webapi"
		bucketName        = "website-test"
		keyName           = "website-key"
		adminTokenName    = "webapi-admin-token"
		webRootDomain     = ".web.garage.local"
		expectedIndexHTML = "<html><body><h1>Hello from Garage!</h1></body></html>"
		expectedErrorHTML = "<html><body><h1>404 - Page Not Found</h1></body></html>"
	)

	BeforeAll(func() {
		By("applying WebAPI test resources")
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(webAPITestResources(clusterName, bucketName, keyName, adminTokenName, webRootDomain))
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply WebAPI test resources")
	})

	AfterAll(func() {
		By("cleaning up WebAPI test resources")
		// Delete in reverse order of dependencies
		cmd := exec.Command("kubectl", "delete", "garagekey", keyName, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		cmd = exec.Command("kubectl", "delete", "garagebucket", bucketName, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		cmd = exec.Command("kubectl", "delete", "garageadmintoken", adminTokenName, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		cmd = exec.Command("kubectl", "delete", "garagecluster", clusterName, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		// Wait for cluster resources to be cleaned up
		time.Sleep(5 * time.Second)
	})

	Context("Website Hosting", func() {
		It("should deploy GarageCluster with WebAPI enabled", func() {
			By("waiting for GarageCluster to be ready")
			verifyClusterReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "garagecluster", clusterName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// GarageCluster uses "Running" phase, not "Ready"
				g.Expect(output).To(Equal("Running"), "GarageCluster not ready yet")
			}
			Eventually(verifyClusterReady, 5*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying all Garage pods are running")
			verifyPodsRunning := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-l", fmt.Sprintf("app.kubernetes.io/instance=%s", clusterName),
					"-n", namespace, "-o", "jsonpath={.items[*].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				phases := strings.Fields(output)
				g.Expect(phases).To(HaveLen(1), "Expected 1 Garage pod") // Single replica for testing
				for _, phase := range phases {
					g.Expect(phase).To(Equal("Running"), "Pod not running")
				}
			}
			Eventually(verifyPodsRunning, 3*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should create GarageBucket with website hosting enabled", func() {
			By("waiting for GarageBucket to be ready")
			verifyBucketReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "garagebucket", bucketName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Ready"), "GarageBucket not ready yet")
			}
			Eventually(verifyBucketReady, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying website hosting is enabled")
			verifyWebsiteEnabled := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "garagebucket", bucketName, "-n", namespace,
					"-o", "jsonpath={.status.websiteEnabled}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("true"), "Website hosting not enabled")
			}
			Eventually(verifyWebsiteEnabled, time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should create GarageKey with bucket permissions", func() {
			By("waiting for GarageKey to be ready")
			verifyKeyReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "garagekey", keyName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Ready"), "GarageKey not ready yet")
			}
			Eventually(verifyKeyReady, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying key secret was created")
			cmd := exec.Command("kubectl", "get", "secret", keyName, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Key secret should exist")
		})

		It("should upload content to bucket via S3 API", func() {
			By("getting S3 credentials from key secret")
			var accessKeyID, secretAccessKey string

			getCredentials := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", keyName, "-n", namespace,
					"-o", "jsonpath={.data.access-key-id}")
				accessKeyIDBase64, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				cmd = exec.Command("kubectl", "get", "secret", keyName, "-n", namespace,
					"-o", "jsonpath={.data.secret-access-key}")
				secretAccessKeyBase64, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// Decode base64
				accessKeyIDBytes, err := base64.StdEncoding.DecodeString(accessKeyIDBase64)
				g.Expect(err).NotTo(HaveOccurred())
				accessKeyID = string(accessKeyIDBytes)

				secretAccessKeyBytes, err := base64.StdEncoding.DecodeString(secretAccessKeyBase64)
				g.Expect(err).NotTo(HaveOccurred())
				secretAccessKey = string(secretAccessKeyBytes)

				g.Expect(accessKeyID).NotTo(BeEmpty(), "Access key ID should not be empty")
				g.Expect(secretAccessKey).NotTo(BeEmpty(), "Secret access key should not be empty")
			}
			Eventually(getCredentials, time.Minute, 5*time.Second).Should(Succeed())

			By("uploading index.html via S3 API using a job")
			indexUploadJob := s3UploadJob("upload-index", namespace, clusterName, bucketName,
				accessKeyID, secretAccessKey, "index.html", expectedIndexHTML, "text/html")
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(indexUploadJob)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create index upload job")

			By("waiting for index upload job to complete")
			verifyIndexUpload := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "upload-index", "-n", namespace,
					"-o", "jsonpath={.status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("1"), "Index upload job not completed")
			}
			Eventually(verifyIndexUpload, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("uploading error.html via S3 API using a job")
			errorUploadJob := s3UploadJob("upload-error", namespace, clusterName, bucketName,
				accessKeyID, secretAccessKey, "error.html", expectedErrorHTML, "text/html")
			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(errorUploadJob)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create error upload job")

			By("waiting for error upload job to complete")
			verifyErrorUpload := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "job", "upload-error", "-n", namespace,
					"-o", "jsonpath={.status.succeeded}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("1"), "Error upload job not completed")
			}
			Eventually(verifyErrorUpload, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should serve index.html via Web API", func() {
			By("accessing website root via Web API with correct Host header")
			verifyIndexServed := func(g Gomega) {
				// Access the Web API with the bucket's vhost
				// The Host header should be: <bucket>.<rootDomain> (without leading dot)
				webHost := bucketName + strings.TrimPrefix(webRootDomain, ".")
				webServiceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:3902/", clusterName, namespace)

				// Create a pod to curl the web endpoint with the correct Host header
				curlPodName := "curl-webapi-index"
				curlPod := curlWebAPIPod(curlPodName, namespace, webServiceURL, webHost)
				cmd := exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(curlPod)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// Wait for pod to complete
				cmd = exec.Command("kubectl", "wait", "pod", curlPodName, "-n", namespace,
					"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s")
				_, err = utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Curl pod did not succeed")

				// Get the response
				cmd = exec.Command("kubectl", "logs", curlPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Hello from Garage!"), "Index page not served correctly")
			}
			Eventually(verifyIndexServed, 2*time.Minute, 10*time.Second).Should(Succeed())

			// Cleanup curl pod
			cmd := exec.Command("kubectl", "delete", "pod", "curl-webapi-index", "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should serve error.html for non-existent paths", func() {
			By("accessing non-existent path via Web API")
			verifyErrorServed := func(g Gomega) {
				webHost := bucketName + strings.TrimPrefix(webRootDomain, ".")
				webServiceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:3902/nonexistent-page.html", clusterName, namespace)

				curlPodName := "curl-webapi-error"
				curlPod := curlWebAPIPod(curlPodName, namespace, webServiceURL, webHost)
				cmd := exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(curlPod)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// Wait for pod to complete
				cmd = exec.Command("kubectl", "wait", "pod", curlPodName, "-n", namespace,
					"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s")
				_, err = utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Curl pod did not succeed")

				// Get the response
				cmd = exec.Command("kubectl", "logs", curlPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("404 - Page Not Found"), "Error page not served correctly")
			}
			Eventually(verifyErrorServed, 2*time.Minute, 10*time.Second).Should(Succeed())

			// Cleanup curl pod
			cmd := exec.Command("kubectl", "delete", "pod", "curl-webapi-error", "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should return 404 for unknown bucket hosts", func() {
			By("accessing Web API with unknown bucket host")
			verifyUnknownBucket := func(g Gomega) {
				// Use a bucket that doesn't exist
				webHost := "nonexistent-bucket" + strings.TrimPrefix(webRootDomain, ".")
				webServiceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:3902/", clusterName, namespace)

				curlPodName := "curl-webapi-unknown"
				curlPod := curlWebAPIPodWithStatus(curlPodName, namespace, webServiceURL, webHost)
				cmd := exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(curlPod)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())

				// Wait for pod to complete
				cmd = exec.Command("kubectl", "wait", "pod", curlPodName, "-n", namespace,
					"--for=jsonpath={.status.phase}=Succeeded", "--timeout=60s")
				_, err = utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Curl pod did not succeed")

				// Get the response (includes HTTP status)
				cmd = exec.Command("kubectl", "logs", curlPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				// Should return 404 for unknown bucket
				g.Expect(output).To(ContainSubstring("404"), "Should return 404 for unknown bucket")
			}
			Eventually(verifyUnknownBucket, 2*time.Minute, 10*time.Second).Should(Succeed())

			// Cleanup curl pod
			cmd := exec.Command("kubectl", "delete", "pod", "curl-webapi-unknown", "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		AfterAll(func() {
			By("cleaning up S3 upload jobs")
			cmd := exec.Command("kubectl", "delete", "job", "upload-index", "upload-error", "-n", namespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})
})

// webAPITestResources returns the YAML for all test resources
func webAPITestResources(clusterName, bucketName, keyName, adminTokenName, webRootDomain string) string {
	return fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %[1]s-rpc-secret
  namespace: %[5]s
type: Opaque
data:
  # 32-byte hex secret for RPC
  rpc-secret: YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWYwMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1Njc4OQ==
---
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageAdminToken
metadata:
  name: %[4]s
  namespace: %[5]s
spec:
  clusterRef:
    name: %[1]s
---
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageCluster
metadata:
  name: %[1]s
  namespace: %[5]s
spec:
  replicas: 1
  image: "dxflrs/garage:v2.1.0"
  zone: "test-zone"
  replication:
    factor: 1
  storage:
    metadata:
      size: 1Gi
    data:
      size: 5Gi
  network:
    rpcBindPort: 3901
    rpcSecretRef:
      name: %[1]s-rpc-secret
      key: rpc-secret
  s3Api:
    enabled: true
    bindPort: 3900
    region: garage
  webApi:
    enabled: true
    bindPort: 3902
    rootDomain: "%[6]s"
  admin:
    enabled: true
    bindPort: 3903
    adminTokenSecretRef:
      name: %[4]s
      key: admin-token
  resources:
    requests:
      memory: "256Mi"
      cpu: "100m"
    limits:
      memory: "512Mi"
      cpu: "500m"
---
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageBucket
metadata:
  name: %[2]s
  namespace: %[5]s
spec:
  clusterRef:
    name: %[1]s
  globalAlias: %[2]s
  website:
    enabled: true
    indexDocument: index.html
    errorDocument: error.html
  keyPermissions:
    - keyRef: %[3]s
      read: true
      write: true
      owner: true
---
apiVersion: garage.rajsingh.info/v1alpha1
kind: GarageKey
metadata:
  name: %[3]s
  namespace: %[5]s
spec:
  clusterRef:
    name: %[1]s
  bucketPermissions:
    - bucketRef: %[2]s
      read: true
      write: true
      owner: true
  secretTemplate:
    name: %[3]s
    includeEndpoint: true
    includeRegion: true
`, clusterName, bucketName, keyName, adminTokenName, namespace, webRootDomain)
}

// s3UploadJob creates a Kubernetes Job that uploads content to S3
func s3UploadJob(jobName, namespace, clusterName, bucketName, accessKeyID, secretAccessKey, objectKey, content, contentType string) string {
	// Base64 encode the content to safely pass it
	contentBase64 := base64.StdEncoding.EncodeToString([]byte(content))

	return fmt.Sprintf(`---
apiVersion: batch/v1
kind: Job
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  ttlSecondsAfterFinished: 300
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: upload
        image: amazon/aws-cli:latest
        env:
        - name: AWS_ACCESS_KEY_ID
          value: "%[5]s"
        - name: AWS_SECRET_ACCESS_KEY
          value: "%[6]s"
        - name: AWS_DEFAULT_REGION
          value: "garage"
        command:
        - /bin/sh
        - -c
        - |
          echo "%[8]s" | base64 -d > /tmp/content.html
          aws --endpoint-url http://%[3]s.%[2]s.svc.cluster.local:3900 \
            s3 cp /tmp/content.html s3://%[4]s/%[7]s \
            --content-type "%[9]s"
        securityContext:
          readOnlyRootFilesystem: false
          allowPrivilegeEscalation: false
          runAsNonRoot: true
          runAsUser: 1000
          seccompProfile:
            type: RuntimeDefault
          capabilities:
            drop:
            - ALL
`, jobName, namespace, clusterName, bucketName, accessKeyID, secretAccessKey, objectKey, contentBase64, contentType)
}

// curlWebAPIPod creates a pod that curls the web API with a specific Host header
func curlWebAPIPod(podName, namespace, url, host string) string {
	return fmt.Sprintf(`---
apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  restartPolicy: Never
  containers:
  - name: curl
    image: curlimages/curl:latest
    command:
    - curl
    - -s
    - -H
    - "Host: %[4]s"
    - "%[3]s"
    securityContext:
      readOnlyRootFilesystem: true
      allowPrivilegeEscalation: false
      runAsNonRoot: true
      runAsUser: 1000
      seccompProfile:
        type: RuntimeDefault
      capabilities:
        drop:
        - ALL
`, podName, namespace, url, host)
}

// curlWebAPIPodWithStatus creates a pod that curls the web API and includes HTTP status
func curlWebAPIPodWithStatus(podName, namespace, url, host string) string {
	return fmt.Sprintf(`---
apiVersion: v1
kind: Pod
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  restartPolicy: Never
  containers:
  - name: curl
    image: curlimages/curl:latest
    command:
    - curl
    - -s
    - -w
    - "\\nHTTP_STATUS:%%{http_code}\\n"
    - -H
    - "Host: %[4]s"
    - "%[3]s"
    securityContext:
      readOnlyRootFilesystem: true
      allowPrivilegeEscalation: false
      runAsNonRoot: true
      runAsUser: 1000
      seccompProfile:
        type: RuntimeDefault
      capabilities:
        drop:
        - ALL
`, podName, namespace, url, host)
}
