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

package validate

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/kubernetes-sigs/cri-tools/pkg/framework"
	internalapi "k8s.io/kubernetes/pkg/kubelet/apis/cri"
	runtimeapi "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = framework.KubeDescribe("Image Manager", func() {
	f := framework.NewDefaultCRIFramework()

	var c internalapi.ImageManagerService

	BeforeEach(func() {
		c = f.CRIClient.CRIImageClient
	})

	It("public image with tag should be pulled and removed [Conformance]", func() {
		testPullPublicImage(c, testImageWithTag, testImagePodSandbox, func(s *runtimeapi.Image) {
			Expect(s.RepoTags).To(Equal([]string{testImageWithTag}))
		})
	})

	It("public image without tag should be pulled and removed [Conformance]", func() {
		testPullPublicImage(c, testImageWithoutTag, testImagePodSandbox, func(s *runtimeapi.Image) {
			Expect(s.RepoTags).To(Equal([]string{testImageWithoutTag + ":latest"}))
		})
	})

	It("public image with digest should be pulled and removed [Conformance]", func() {
		testPullPublicImage(c, testImageWithDigest, testImagePodSandbox, func(s *runtimeapi.Image) {
			Expect(s.RepoTags).To(BeEmpty())
			Expect(s.RepoDigests).To(Equal([]string{testImageWithDigest}))
		})
	})

	It("image status should support all kinds of references [Conformance]", func() {
		imageName := testImageWithTag
		// Make sure image does not exist before testing.
		removeImage(c, imageName)

		framework.PullPublicImage(c, imageName, testImagePodSandbox)

		status := framework.ImageStatus(c, imageName)
		Expect(status).NotTo(BeNil(), "should get image status")
		idStatus := framework.ImageStatus(c, status.GetId())
		Expect(idStatus).To(Equal(status), "image status with %q", status.GetId())
		for _, tag := range status.GetRepoTags() {
			tagStatus := framework.ImageStatus(c, tag)
			Expect(tagStatus).To(Equal(status), "image status with %q", tag)
		}
		for _, digest := range status.GetRepoDigests() {
			digestStatus := framework.ImageStatus(c, digest)
			Expect(digestStatus).To(Equal(status), "image status with %q", digest)
		}

		testRemoveImage(c, imageName)

	})

	if runtime.GOOS != "windows" || framework.TestContext.IsLcow {
		It("image status get image fields should not have Uid|Username empty [Conformance]", func() {
			for _, item := range []struct {
				description string
				image       string
				uid         int64
				username    string
			}{
				{
					description: "UID only",
					image:       testImageUserUID,
					uid:         imageUserUID,
					username:    "",
				},
				{
					description: "Username only",
					image:       testImageUserUsername,
					uid:         int64(0),
					username:    imageUserUsername,
				},
				{
					description: "UID:group",
					image:       testImageUserUIDGroup,
					uid:         imageUserUIDGroup,
					username:    "",
				},
				{
					description: "Username:group",
					image:       testImageUserUsernameGroup,
					uid:         int64(0),
					username:    imageUserUsernameGroup,
				},
			} {
				framework.PullPublicImage(c, item.image, testImagePodSandbox)
				defer removeImage(c, item.image)

				status := framework.ImageStatus(c, item.image)
				Expect(status.GetUid().GetValue()).To(Equal(item.uid), fmt.Sprintf("%s, Image Uid should be %d", item.description, item.uid))
				Expect(status.GetUsername()).To(Equal(item.username), fmt.Sprintf("%s, Image Username should be %s", item.description, item.username))
			}
		})
	}

	It("listImage should get exactly 3 image in the result list [Conformance]", func() {
		// Make sure test image does not exist.
		removeImageList(c, testDifferentTagDifferentImageList)
		ids := pullImageList(c, testDifferentTagDifferentImageList, testImagePodSandbox)
		ids = removeDuplicates(ids)
		Expect(len(ids)).To(Equal(3), "3 image ids should be returned")

		defer removeImageList(c, testDifferentTagDifferentImageList)

		images := framework.ListImage(c, &runtimeapi.ImageFilter{})

		for i, id := range ids {
			for _, img := range images {
				if img.Id == id {
					Expect(len(img.RepoTags)).To(Equal(1), "Should only have 1 repo tag")
					Expect(img.RepoTags[0]).To(Equal(testDifferentTagDifferentImageList[i]), "Repo tag should be correct")
					break
				}
			}
		}
	})

	It("listImage should get exactly 3 repoTags in the result image [Conformance]", func() {
		// Make sure test image does not exist.
		removeImageList(c, testDifferentTagSameImageList)
		ids := pullImageList(c, testDifferentTagSameImageList, testImagePodSandbox)
		ids = removeDuplicates(ids)
		Expect(len(ids)).To(Equal(1), "Only 1 image id should be returned")

		defer removeImageList(c, testDifferentTagSameImageList)

		images := framework.ListImage(c, &runtimeapi.ImageFilter{})

		sort.Strings(testDifferentTagSameImageList)
		for _, img := range images {
			if img.Id == ids[0] {
				sort.Strings(img.RepoTags)
				Expect(img.RepoTags).To(Equal(testDifferentTagSameImageList), "Should have 3 repoTags in single image")
				break
			}
		}
	})
})

var _ = framework.KubeOptionalDescribe("Image Manager", func() {
	f := framework.NewDefaultCRIFramework()

	var (
		rc internalapi.RuntimeService
		ic internalapi.ImageManagerService
	)

	BeforeEach(func() {
		rc = f.CRIClient.CRIRuntimeClient
		ic = f.CRIClient.CRIImageClient
	})

	Context("parellel image pulling [Stress]", func() {
		testImageList := []string{
			"wordpress",
			"mongo",
			"ghost",
			"docker",
			"rabbitmq",
			"perl",
			"rocket.chat",
			"elixir",
			"node",
			"opensuse",
			"mariadb",
			"memcached",
			"hylang",
			"haproxy",
			"erlang",
			"maven",
			"drupal",
			"websphere-liberty",
			"open-liberty",
			"adoptopenjdk",
			"ibmjava",
			"gazebo",
			"solr",
			"tomee",
			"pypy",
			"zookeeper",
			"tomcat",
			"sonarqube",
			"rapidoid",
			"nuxeo",
			"orientdb",
			"gradle",
			"jruby",
			"groovy",
			"jetty",
			"lightstreamer",
			"flink",
			"kaazing-gateway",
			"clojure",
			"openjdk",
			"express-gateway",
			"arangodb",
			"ros",
			"xwiki",
			"teamspeak",
			"percona",
			"crate",
			"alt",
			"telegraf",
			"influxdb",
			"kapacitor",
			"chronograf",
			"rust",
			"consul",
			"swipl",
			"photon",
			"amazonlinux",
			"amazoncorretto",
			"logstash:7.1.0",
			"kibana:7.1.0",
			"elasticsearch:7.1.0",
			"python",
			"julia",
			"golang",
			"sourcemage",
			"mageia",
			"haskell",
			"nextcloud",
			"ruby",
			"redis",
			"geonetwork",
			"buildpack-deps",
			"swift",
			"bonita",
			"ubuntu",
			"thrift",
			"silverpeas",
			"php-zendserver",
			"neurodebian",
			"couchbase",
			"storm",
			"clearlinux",
			"yourls",
			"joomla",
			"postfixadmin",
			"matomo",
			"adminer",
			"convertigo",
			"mongo-express",
			"composer",
			"postgres",
			"bash",
			"php",
			"httpd",
			"spiped",
			"nginx",
			"fluentd",
			"alpine",
			"haxe",
			"neo4j",
		}

		BeforeEach(func() {
			for _, imageName := range testImageList {
				imageSpec := &runtimeapi.ImageSpec{
					Image: imageName,
				}
				ic.RemoveImage(imageSpec)
			}
		})

		AfterEach(func() {
			for _, imageName := range testImageList {
				imageSpec := &runtimeapi.ImageSpec{
					Image: imageName,
				}
				ic.RemoveImage(imageSpec)
			}
		})

		It("should be stable", func() {
			var wg sync.WaitGroup
			wg.Add(len(testImageList))
			for _, i := range testImageList {
				go func(image string) {
					defer GinkgoRecover()
					defer func() {
						wg.Done()
					}()

					podID, podConfig := framework.CreatePodSandboxForContainer(rc)
					defer func() {
						Expect(rc.StopPodSandbox(podID)).To(Succeed(), "stop pod sandbox", image)
						Expect(rc.RemovePodSandbox(podID)).To(Succeed(), "remove pod sandbox", image)
					}()

					framework.PullPublicImage(ic, image, podConfig)

					containerConfig := &runtimeapi.ContainerConfig{
						Metadata: framework.BuildContainerMetadata(image, framework.DefaultAttempt),
						Image:    &runtimeapi.ImageSpec{Image: image},
						Command:  []string{"ls", "/"},
					}

					containerID := framework.CreateContainer(rc, ic, containerConfig, podID, podConfig)
					Expect(rc.StartContainer(containerID)).To(Succeed(), "start container", image)

					var (
						status *runtimeapi.ContainerStatus
						err    error
					)
					Eventually(func() runtimeapi.ContainerState {
						status, err = rc.ContainerStatus(containerID)
						Expect(err).NotTo(HaveOccurred(), "container status", image)
						return status.GetState()
					}, 2*time.Minute, time.Second*4).Should(Equal(runtimeapi.ContainerState_CONTAINER_EXITED), "wait container exits", image)

					Expect(status.GetExitCode()).To(BeZero(), "container exit code", image)
				}(i)
			}

			wg.Wait()
		})
	})

})

// testRemoveImage removes the image name imageName and check if it successes.
func testRemoveImage(c internalapi.ImageManagerService, imageName string) {
	By("Remove image : " + imageName)
	removeImage(c, imageName)

	By("Check image list empty")
	status := framework.ImageStatus(c, imageName)
	Expect(status).To(BeNil(), "Should have none image in list")
}

// testPullPublicImage pulls the image named imageName, make sure it success and remove the image.
func testPullPublicImage(c internalapi.ImageManagerService, imageName string, podConfig *runtimeapi.PodSandboxConfig, statusCheck func(*runtimeapi.Image)) {
	// Make sure image does not exist before testing.
	removeImage(c, imageName)

	framework.PullPublicImage(c, imageName, podConfig)

	By("Check image list to make sure pulling image success : " + imageName)
	status := framework.ImageStatus(c, imageName)
	Expect(status).NotTo(BeNil(), "Should have one image in list")
	Expect(status.Id).NotTo(BeNil(), "Image Id should not be nil")
	Expect(status.Size_).NotTo(BeNil(), "Image Size should not be nil")
	if statusCheck != nil {
		statusCheck(status)
	}

	testRemoveImage(c, imageName)
}

// pullImageList pulls the images listed in the imageList.
func pullImageList(c internalapi.ImageManagerService, imageList []string, podConfig *runtimeapi.PodSandboxConfig) []string {
	var ids []string
	for _, imageName := range imageList {
		ids = append(ids, framework.PullPublicImage(c, imageName, podConfig))
	}
	return ids
}

// removeImageList removes the images listed in the imageList.
func removeImageList(c internalapi.ImageManagerService, imageList []string) {
	for _, imageName := range imageList {
		removeImage(c, imageName)
	}
}

// removeImage removes the image named imagesName.
func removeImage(c internalapi.ImageManagerService, imageName string) {
	By("Remove image : " + imageName)
	image, err := c.ImageStatus(&runtimeapi.ImageSpec{Image: imageName})
	framework.ExpectNoError(err, "failed to get image status: %v", err)

	if image != nil {
		By("Remove image by ID : " + image.Id)
		err = c.RemoveImage(&runtimeapi.ImageSpec{Image: image.Id})
		framework.ExpectNoError(err, "failed to remove image: %v", err)
	}
}

// removeDuplicates remove duplicates strings from a list
func removeDuplicates(ss []string) []string {
	encountered := map[string]bool{}
	result := []string{}
	for _, s := range ss {
		if encountered[s] == true {
			continue
		}
		encountered[s] = true
		result = append(result, s)
	}
	return result
}
