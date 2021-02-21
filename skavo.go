package main

import (
	"fmt"
	"github.com/AlecAivazis/survey/v2"
	v1 "k8s.io/api/core/v1"
)

func main() {

	client := NewK8sClient()
	podList := client.ListPods()

	pod := selectPod(podList.Items)

	print("Selected pod: "+pod.Name)

}

func selectPod(pods []v1.Pod) v1.Pod{
	podNames := make([]string, len(pods))

	for i, pod := range pods {
		podNames[i] = pod.Name
	}

	p := &survey.Select{
		Message: "Choose a Pod:",
		Options: podNames,
	}
	i := new(int)

	err := survey.AskOne(p, i)

	if err != nil {
		panic(fmt.Errorf("failed to create clientset %w", err))
	}
	return pods[*i]
}
