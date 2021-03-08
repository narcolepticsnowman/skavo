package delve

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/narcolepticsnowman/go-mirror/mirror"

	"github.com/narcolepticsnowman/skavo/pkg/k8s"
)

type PodDelve struct {
	Namespace     string
	PodName       string
	ContainerName string
	Process       k8s.ContainerProcess
	Client        *k8s.Client
	LocalPort     string
	PodPort       string
}

func (pd *PodDelve) InstallDelve() {
	fmt.Println("Installing Delve...")
	pd.runScript(installDelve, "installDelve.sh")
}

func (pd *PodDelve) ForwardPort() {
	fmt.Printf("Forwarding local port %s to remote port %s\n", pd.LocalPort, pd.PodPort)
	<-pd.Client.ForwardPort(pd.Namespace, pd.PodName, pd.LocalPort, pd.PodPort)
}

func (pd *PodDelve) RestartProcess() {
	pd.InstallDelve()
	fmt.Printf("Relaunching pid %d with delve\n", pd.Process.Pid)
	go func() {
		args := append([]string{pd.PodPort, strconv.Itoa(pd.Process.Pid)}, strings.Split(pd.Process.Command, " ")...)
		pd.runScript(delveExec, "delveExec.sh", args...)
	}()
	pd.ForwardPort()
}

func hasRefs(refs []metav1.OwnerReference) bool {
	return refs != nil && len(refs) > 0
}

func (pd *PodDelve) readyCount(kind string, name string) (int32, error) {
	resource := pd.getResource(kind, name)
	switch kind {
	case "Deployment":
		return resource.(*appsv1.Deployment).Status.ReadyReplicas, nil
	case "StatefulSet":
		return resource.(*appsv1.StatefulSet).Status.ReadyReplicas, nil
	case "DaemonSet":
		return resource.(*appsv1.DaemonSet).Status.NumberAvailable, nil
	case "ReplicaSet":
		return resource.(*appsv1.ReplicaSet).Status.ReadyReplicas, nil
	default:
		return 0, fmt.Errorf("unexpected Kind: %s", kind)
	}
}

func (pd *PodDelve) getResource(kind string, name string) runtime.Object {
	var res runtime.Object
	var err error
	switch kind {
	case "Deployment":
		res, err = pd.Client.AppsClient.Deployments(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	case "StatefulSet":
		res, err = pd.Client.AppsClient.StatefulSets(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	case "DaemonSet":
		res, err = pd.Client.AppsClient.DaemonSets(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	case "ReplicaSet":
		res, err = pd.Client.AppsClient.ReplicaSets(pd.Namespace).Get(context.TODO(), name, metav1.GetOptions{})
	default:
		panic(fmt.Errorf("unexpected Kind: %s", kind))
	}
	if err != nil {
		panic(fmt.Errorf("failed to load resource of kind %s with name %s: %+v", kind, name, err))
	}
	return res
}

func (pd *PodDelve) updateResource(kind string, obj runtime.Object) runtime.Object {
	var res runtime.Object
	var err error
	switch kind {
	case "Deployment":
		res, err = pd.Client.AppsClient.Deployments(pd.Namespace).Update(context.TODO(), obj.(*appsv1.Deployment), metav1.UpdateOptions{})
	case "StatefulSet":
		res, err = pd.Client.AppsClient.StatefulSets(pd.Namespace).Update(context.TODO(), obj.(*appsv1.StatefulSet), metav1.UpdateOptions{})
	case "DaemonSet":
		res, err = pd.Client.AppsClient.DaemonSets(pd.Namespace).Update(context.TODO(), obj.(*appsv1.DaemonSet), metav1.UpdateOptions{})
	case "ReplicaSet":
		res, err = pd.Client.AppsClient.ReplicaSets(pd.Namespace).Update(context.TODO(), obj.(*appsv1.ReplicaSet), metav1.UpdateOptions{})
	default:
		panic(fmt.Errorf("unexpected Kind: %s", kind))
	}
	if err != nil {
		panic(fmt.Errorf("failed to update resource of kind %s: %+v", kind, err))
	}
	return res
}

func objectName(r runtime.Object) string {
	return mirror.Reflect(r).GetPath("Name").Value().String()
}

func (pd *PodDelve) UpdateResource(obj runtime.Object) runtime.Object {
	reflector := mirror.Reflect(obj)
	kind := reflector.GetPath("/Kind").Value().String()
	var client *mirror.Reflection
	if kind == "Pod" {
		client = mirror.Reflect(pd.Client.CoreClient)
	} else {
		client = mirror.Reflect(pd.Client.AppsClient)
	}

	ret := client.GetPath(kind + "s").Exec(pd.Namespace).Ret()[0].GetPath("Update").
		Exec(context.TODO(), obj, metav1.UpdateOptions{}).Ret()
	err := ret[1].Value().Interface().(error)
	if err != nil {
		panic(fmt.Errorf("failed to update resource of kind %s: %+v", kind, err))
	}
	return ret[0].Value().Interface().(runtime.Object)
}

func (pd *PodDelve) Relaunch(pod *v1.Pod) {
	kind, resource := pd.getRootResource(pod)
	pd.addSkavoAnnotations(resource)
	pd.createEntryPointConfigMap(pod.Namespace)

	if kind == "Pod" {
		_, err := pd.Client.CoreClient.Pods(pod.Namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		if err != nil {
			panic(fmt.Errorf("failed to updated Pod: %+v", err))
		}
	} else {

		resource = pd.updateResource(kind, resource)
		cnt, err := pd.readyCount(kind, objectName(resource))
		for cnt < 1 {
			if err != nil {
				panic(fmt.Errorf("failed to check read count: %+v", err))
			}
			time.Sleep(1 * time.Second)
			cnt, err = pd.readyCount(kind, objectName(resource))
		}
		resource = pd.getResource(kind, objectName(resource))

		selector, err := metav1.LabelSelectorAsSelector(mirror.Reflect(resource).GetPath("/Spec/Selector").Value().Interface().(*metav1.LabelSelector))
		if err != nil {
			panic(fmt.Errorf("failed to make selector: %+v", err))
		}
		podList, err := pd.Client.CoreClient.Pods(pd.Namespace).List(context.TODO(), metav1.ListOptions{
			TypeMeta:      metav1.TypeMeta{},
			LabelSelector: selector.String(),
			Limit:         1,
		})
		if err != nil {
			panic(fmt.Errorf("failed to get pod list: %+v", err))
		}
		pd.PodName = podList.Items[0].Name
		pd.ForwardPort()
	}
}

func (pd *PodDelve) addSkavoAnnotations(resource runtime.Object) {
	meta := mirror.Reflect(resource).GetPath("/ObjectMeta").Value().Interface().(metav1.ObjectMeta)
	annotations := meta.GetAnnotations()
	annotations["skavo.container"] = pd.ContainerName
	annotations["skavo.cmd"] = skavoEntrypointShName
	annotations["skavo.args"] = pd.PodPort + " " + pd.Process.Command
	annotations["skavo.cfgMap"] = configMapName
	meta.SetAnnotations(annotations)
}

func (pd *PodDelve) AttachToProcess() {
	pd.InstallDelve()
	fmt.Printf("Attaching to Process: %+v\n", pd.Process)
	go func() {
		pd.runScript(delveAttach, "delveAttach.sh", pd.PodPort, strconv.Itoa(pd.Process.Pid))
	}()
	pd.ForwardPort()
}

func (pd *PodDelve) Exec(cmd ...string) {
	pd.Client.Exec(
		pd.PodName,
		pd.Namespace,
		pd.ContainerName,
		cmd,
		k8s.ExecOptions{
			Out:    os.Stdout,
			In:     nil,
			ErrOut: os.Stderr,
		},
	)
}

func (pd *PodDelve) ExecWrite(in io.Reader, cmd ...string) {
	pd.Client.Exec(
		pd.PodName,
		pd.Namespace,
		pd.ContainerName,
		cmd,
		k8s.ExecOptions{
			Out:    nil,
			In:     in,
			ErrOut: nil,
		},
	)
}

func (pd *PodDelve) runScript(src string, name string, args ...string) {
	pd.ExecWrite(strings.NewReader(src), "sh", "-c", "cat /dev/stdin > /"+name)
	pd.Exec("sh", "-c", "sh /"+name+" "+strings.Join(args, " ")+" 2>&1 &")
}

func (pd *PodDelve) getRootResource(pod *v1.Pod) (string, runtime.Object) {
	var root runtime.Object
	kind := ""
	root = pod
	var ownerRef *metav1.OwnerReference
	if hasRefs(pod.OwnerReferences) {
		ownerRef = &pod.OwnerReferences[0]
		for ownerRef != nil {
			kind = ownerRef.Kind
			owner := pd.getResource(ownerRef.Kind, ownerRef.Name)
			switch ownerRef.Kind {
			case "Deployment":
				fallthrough
			case "StatefulSet":
				fallthrough
			case "DaemonSet":
				root = owner
				ownerRef = nil
			case "ReplicaSet":
				if hasRefs(owner.(*appsv1.ReplicaSet).OwnerReferences) {
					ownerRef = &owner.(*appsv1.ReplicaSet).OwnerReferences[0]
				} else {
					root = owner
					ownerRef = nil
				}
			default:
				panic(fmt.Errorf("unexpected owner kind:%s", ownerRef.Kind))
			}
		}
	}
	return kind, root
}

func (pd *PodDelve) createEntryPointConfigMap(namespace string) {
	configMap, err := pd.Client.CoreClient.ConfigMaps(namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if err != nil || configMap == nil {
		key, cert, err := GenerateSelfCaSignedTLSCertFiles(namespace)
		if err != nil {
			panic(fmt.Errorf("failed to create self signed cert: %+v", err))
		}
		immutable := false
		data := make(map[string]string)
		data[skavoEntrypointShName] = skavoEntrypoint
		binaryData := make(map[string][]byte)
		binaryData["webhook-tls-key"] = key
		binaryData["webhook-tls-cert"] = cert

		configMap = &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: namespace,
			},
			Immutable:  &immutable,
			Data:       data,
			BinaryData: binaryData,
		}
		_, err = pd.Client.CoreClient.ConfigMaps(namespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
		if err != nil {
			panic(fmt.Errorf("failed to create config map: %+v", err))
		} else {
			fmt.Printf("Created ConfigMap %s", configMapName)
		}
	}
}

func (pd *PodDelve) deployAdmissionWebhook(namespace string) {
	configMap, err := pd.Client.CoreClient.ConfigMaps(namespace).Get(context.TODO(), configMapName, metav1.GetOptions{})
	if err != nil || configMap == nil {
		immutable := false
		data := make(map[string]string)
		data[skavoEntrypointShName] = skavoEntrypoint

		configMap = &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: namespace,
			},
			Immutable: &immutable,
			Data:      data,
		}
		_, err := pd.Client.CoreClient.ConfigMaps(namespace).Create(context.TODO(), configMap, metav1.CreateOptions{})
		if err != nil {
			panic(fmt.Errorf("failed to create config map: %+v", err))
		} else {
			fmt.Printf("Created ConfigMap %s", configMapName)
		}
	}
}

const (
	configMapName         = "skavo-entrypoint-sh"
	skavoEntrypointShName = "/skavoEntrypoint.sh"
)
