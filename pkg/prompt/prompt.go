package prompt

import (
	"fmt"
	"os"
	"regexp"

	"github.com/AlecAivazis/survey/v2"
	v1 "k8s.io/api/core/v1"

	"github.com/narcolepticsnowman/skavo/pkg/k8s"
)

func SelectPod(pods []v1.Pod) *v1.Pod {
	podNames := make([]string, len(pods))
	for i, pod := range pods {
		podNames[i] = pod.Name
	}
	if len(pods) < 1 {
		panic("no pods found")
	}
	return &pods[GetSelection("Select a Pod:", podNames)]
}

func SelectContainer(containers []v1.Container) v1.Container {
	if len(containers) < 2 {
		fmt.Println("One container found")
		return containers[0]
	}
	containerNames := make([]string, len(containers))
	for i, container := range containers {
		containerNames[i] = container.Name
	}

	return containers[GetSelection("Select a Container:", containerNames)]
}

func SelectProcess(processList []k8s.ContainerProcess, processFilter string) k8s.ContainerProcess {
	if processFilter != "" {
		filtered := []k8s.ContainerProcess{}
		for _, process := range processList {
			if match, _ := regexp.MatchString(processFilter, process.Command); match {
				filtered = append(filtered, process)
			}
		}
		processList = filtered
	}
	if len(processList) < 2 {
		fmt.Println("One process found")
		return processList[0]
	}
	commands := make([]string, len(processList))
	for i, command := range processList {
		commands[i] = command.Command
	}

	return processList[GetSelection("Select a Process:", commands)]
}

func GetSelection(message string, options []string) int {
	p := &survey.Select{
		Message: message,
		Options: options,
	}
	i := new(int)

	err := survey.AskOne(p, i)

	if err != nil {
		if err.Error() != "interrupt" {
			panic(fmt.Errorf("prompt failed %w", err))
		} else {
			os.Exit(0)
		}

	}
	return *i
}
