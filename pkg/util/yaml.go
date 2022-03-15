/*
Copyright 2021 The KodeRover Authors.

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

package util

import (
	"strings"

	"helm.sh/helm/v3/pkg/releaseutil"
)

const separator = "\n---\n"

func CombineManifests(yamls []string) string {
	var builder strings.Builder
	for _, y := range yamls {
		builder.WriteString(separator + y)
	}

	return builder.String()
}

func SplitManifests(content string) []string {
	var res []string
	manifests := releaseutil.SplitManifests(content)
	for _, m := range manifests {
		res = append(res, m)
	}

	return res
}

// image ccr.ccs.tencentyun.com/koderover/nginx:stable
// return nginx
func GetImageName(image string) string {
	imageNameStr := ""
	imageArr := strings.Split(image, ":")
	if len(imageArr) > 0 {
		imageNameArr := strings.Split(imageArr[0], "/")
		imageNameStr = imageNameArr[len(imageNameArr)-1]
	}
	return imageNameStr
}
