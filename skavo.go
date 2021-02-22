package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/AlecAivazis/survey/v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/homedir"
	"log"
	"path/filepath"
)

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	podName := flag.String("pod", "", "specify the pod instead of prompting")
	containerName := flag.String("container", "", "specify the container instead of prompting")
	namespace := flag.String("namespace", "default", "specify the container instead of prompting")
	flag.Parse()

	client := NewK8sClient(kubeconfig)
	var pod *v1.Pod
	if *podName == "" {
		podList := client.ListPods(*namespace)

		pod := selectPod(podList.Items)
		fmt.Printf("Selected pod: %s\n", pod.Name)
	} else {
		var err error
		pod, err = client.coreClient.Pods(*namespace).Get(context.TODO(), *podName, metav1.GetOptions{})
		if err != nil {
			log.Fatal("Failed to get pod: ", podName, err)
		}
	}
	if *containerName == "" {
		container := selectContainer(pod.Spec.Containers)
		containerName = &container.Name
		fmt.Printf("Selected container: %s\n", container.Name)
	}

	processes := client.ListProcesses(pod, *containerName)

	process := selectProcess(processes)
	fmt.Printf("Attaching to process: %+v\n", process)
}

func selectPod(pods []v1.Pod) *v1.Pod {
	podNames := make([]string, len(pods))
	for i, pod := range pods {
		podNames[i] = pod.Name
	}
	return &pods[getSelection("Select a Pod:", podNames)]
}

func selectContainer(containers []v1.Container) v1.Container {
	if len(containers) < 2 {
		println("Only one container found")
		return containers[0]
	}
	containerNames := make([]string, len(containers))
	for i, container := range containers {
		containerNames[i] = container.Name
	}

	return containers[getSelection("Select a Container:", containerNames)]
}

func selectProcess(processList []ContainerProcess) ContainerProcess {
	if len(processList) < 2 {
		println(" - Only one process found")
		return processList[0]
	}
	commands := make([]string, len(processList))
	for i, command := range processList {
		commands[i] = command.Command
	}

	return processList[getSelection("Select a Process:", commands)]
}

func getSelection(message string, options []string) int {
	p := &survey.Select{
		Message: message,
		Options: options,
	}
	i := new(int)

	err := survey.AskOne(p, i)

	if err != nil {
		panic(fmt.Errorf("prompt failed %w", err))
	}
	return *i
}
