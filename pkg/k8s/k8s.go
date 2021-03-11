package k8s

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	regv1client "k8s.io/client-go/kubernetes/typed/admissionregistration/v1"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	batchv1client "k8s.io/client-go/kubernetes/typed/batch/v1"
	certsv1client "k8s.io/client-go/kubernetes/typed/certificates/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacv1client "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
)

type Client struct {
	CoreClient      *corev1client.CoreV1Client
	AppsClient      *appsv1client.AppsV1Client
	AdmissionClient *regv1client.AdmissionregistrationV1Client
	CertsClient     *certsv1client.CertificatesV1Client
	BatchClient     *batchv1client.BatchV1Client
	RbacClient      *rbacv1client.RbacV1Client
	config          *rest.Config
}

func NewK8sClient(context string, kubeconfig *string) *Client {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: *kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: context},
	).ClientConfig()
	if err != nil {
		panic(fmt.Errorf("failed to build config %w", err))
	}
	return &Client{
		corev1client.NewForConfigOrDie(config),
		appsv1client.NewForConfigOrDie(config),
		regv1client.NewForConfigOrDie(config),
		certsv1client.NewForConfigOrDie(config),
		batchv1client.NewForConfigOrDie(config),
		rbacv1client.NewForConfigOrDie(config),
		config,
	}
}

func (kc *Client) ListPods(namespace string) *v1.PodList {
	pods, err := kc.CoreClient.Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	return pods
}

type ContainerProcess struct {
	Pid     int
	Command string
}

func (kc *Client) ListProcesses(pod *v1.Pod, containerName string) []ContainerProcess {
	out := new(bytes.Buffer)
	kc.Exec(
		pod.Name,
		pod.Namespace,
		containerName,
		[]string{"sh", "-c", "rs=$(printf \"\\036\") && ps -ef|grep -v \"ps -ef\\|xargs\\|tr .\\|tr n\\|<defunct>\"|tr '\\n' \"$rs\"|xargs|tr \"$rs\" '\\n'"},
		ExecOptions{
			Out: out,
		},
	)

	output := out.String()
	lines := strings.Split(output, "\n")
	processes := make([]ContainerProcess, 0)
	for _, line := range lines[1:] {
		line = strings.Trim(line, " \t")
		if strings.Contains(line, "ps -ef") || line == "" {
			continue
		}
		parts := strings.Split(line, " ")
		pid, err := strconv.Atoi(strings.Trim(parts[1], " \t\n"))
		if err != nil {
			panic(err)
		}
		processes = append(processes, ContainerProcess{Pid: pid, Command: strings.Join(parts[7:], " ")})
	}
	return processes
}

type ExecOptions struct {
	//An input stream to send to stdin of the remote command
	In io.Reader
	//An output stream that stdout is received in
	Out io.Writer
	//An output stream that stderr is received in
	ErrOut io.Writer
}

//Execute a command on the given pod
func (kc *Client) Exec(
	podName string,
	namespace string,
	container string,
	command []string,
	options ...ExecOptions,
) {
	var opts ExecOptions
	if len(options) > 0 {
		opts = options[0]
	} else {
		opts = ExecOptions{}
	}

	req := kc.CoreClient.RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&v1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     opts.In != nil,
			Stdout:    opts.Out != nil,
			Stderr:    opts.ErrOut != nil,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(kc.config, "POST", req.URL())
	if err != nil {
		panic(fmt.Errorf("failed to create executor: %+v", err))
	}
	//return p.Executor.Execute("POST", req.URL(), p.Config, p.In, p.Out, p.ErrOut, t.Raw, sizeQueue)
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  opts.In,
		Stdout: opts.Out,
		Stderr: opts.ErrOut,
	})

	if err != nil {
		panic(fmt.Errorf("failed to exec: %+v", err))
	}
}

//mostly borrowed from https://github.com/ica10888/client-go-helper/blob/3402b59130e6b01d2a638942a85a5c4f613c3466/pkg/kubectl/cp.go
func (kc *Client) CopyToPod(namespace string, podName string, containerName string, srcPath string, destPath string) {

	reader, writer := io.Pipe()
	if destPath != "/" && strings.HasSuffix(string(destPath[len(destPath)-1]), "/") {
		destPath = destPath[:len(destPath)-1]
	}

	go func() {
		defer writer.Close()
		makeTar(srcPath, destPath, writer)
	}()
	var cmdArr []string

	cmdArr = []string{"tar", "-xf", "-"}
	destDir := path.Dir(destPath)
	if len(destDir) > 0 {
		cmdArr = append(cmdArr, "-C", destDir)
	}
	kc.Exec(
		podName,
		namespace,
		containerName,
		cmdArr,
		ExecOptions{reader, os.Stdout, os.Stderr},
	)
}

func (kc *Client) ForwardPort(namespace string, podName string, localPort string, podPort string) <-chan struct{} {
	url := kc.CoreClient.RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward").
		URL()
	transport, upgrader, err := spdy.RoundTripperFor(kc.config)
	if err != nil {
		panic(fmt.Errorf("failed round trippin': %+v", err))
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", url)
	stopChan := make(chan struct{})
	readyChan := make(chan struct{})
	fw, err := portforward.NewOnAddresses(dialer, []string{"127.0.0.1"}, []string{localPort + ":" + podPort}, stopChan, readyChan, os.Stdout, os.Stderr)
	if err != nil {
		panic(fmt.Errorf("failed to create port forward: %+v", err))
	}
	err = fw.ForwardPorts()
	if err != nil {
		panic(fmt.Errorf("failed to forward ports: %+v", err))
	}
	fmt.Println("Waiting for port forward to be ready...")
	<-readyChan
	fmt.Println("Ports forwarded!...")
	return stopChan
}

func makeTar(srcPath, destPath string, writer io.Writer) {
	tarWriter := tar.NewWriter(writer)
	defer tarWriter.Close()

	srcPath = path.Clean(srcPath)
	destPath = path.Clean(destPath)
	err := recursiveTar(path.Dir(srcPath), path.Base(srcPath), path.Dir(destPath), path.Base(destPath), tarWriter)
	if err != nil {
		panic(fmt.Errorf("failed to make tar file to send to pod: %+v", err))
	}
}

func recursiveTar(srcBase, srcFile, destBase, destFile string, tw *tar.Writer) error {
	srcPath := path.Join(srcBase, srcFile)
	matchedPaths, err := filepath.Glob(srcPath)
	if err != nil {
		return err
	}
	for _, fpath := range matchedPaths {
		stat, err := os.Lstat(fpath)
		if err != nil {
			return err
		}
		if stat.IsDir() {
			files, err := ioutil.ReadDir(fpath)
			if err != nil {
				return err
			}
			if len(files) == 0 {
				//case empty directory
				hdr, _ := tar.FileInfoHeader(stat, fpath)
				hdr.Name = destFile
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
			}
			for _, f := range files {
				if err := recursiveTar(srcBase, path.Join(srcFile, f.Name()), destBase, path.Join(destFile, f.Name()), tw); err != nil {
					return err
				}
			}
			return nil
		} else if stat.Mode()&os.ModeSymlink != 0 {
			//case soft link
			hdr, _ := tar.FileInfoHeader(stat, fpath)
			target, err := os.Readlink(fpath)
			if err != nil {
				return err
			}

			hdr.Linkname = target
			hdr.Name = destFile
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		} else {
			//case regular file or other file type like pipe
			hdr, err := tar.FileInfoHeader(stat, fpath)
			if err != nil {
				return err
			}
			hdr.Name = destFile

			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}

			f, err := os.Open(fpath)
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
			return f.Close()
		}
	}
	return nil
}
