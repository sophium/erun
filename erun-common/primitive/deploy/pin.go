package deploy

import (
	"strings"
)

func SplitImageTag(image string) (repository, tag string, ok bool) {
	index := strings.LastIndex(image, ":")
	if index <= 0 || index == len(image)-1 {
		return "", "", false
	}
	// A registry port is not a tag separator.
	if strings.Contains(image[index+1:], "/") {
		return "", "", false
	}
	return image[:index], image[index+1:], true
}
