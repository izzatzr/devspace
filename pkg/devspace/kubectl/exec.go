package kubectl

import (
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/devspace-cloud/devspace/pkg/util/terminal"
	"github.com/pkg/errors"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	kubectlExec "k8s.io/client-go/util/exec"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	k8sapi "k8s.io/kubernetes/pkg/apis/core"
	"k8s.io/kubernetes/pkg/kubectl/util/term"
)

// ExecStreamWithTransport executes a kubectl exec with given transport round tripper and upgrader
func ExecStreamWithTransport(transport http.RoundTripper, upgrader spdy.Upgrader, client kubernetes.Interface, pod *k8sv1.Pod, container string, command []string, tty bool, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	var t term.TTY
	var sizeQueue remotecommand.TerminalSizeQueue
	var streamOptions remotecommand.StreamOptions

	execRequest := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")

	if tty {
		t = terminal.SetupTTY(stdin, stdout)

		if t.Raw {
			// this call spawns a goroutine to monitor/update the terminal size
			sizeQueue = t.MonitorSize(t.GetSize())
		}

		streamOptions = remotecommand.StreamOptions{
			Stdin:             stdin,
			Stdout:            stdout,
			Stderr:            stderr,
			Tty:               t.Raw,
			TerminalSizeQueue: sizeQueue,
		}
	} else {
		streamOptions = remotecommand.StreamOptions{
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
		}
	}

	execRequest.VersionedParams(&k8sapi.PodExecOptions{
		Container: container,
		Command:   command,
		Stdin:     stdin != nil,
		Stdout:    stdout != nil,
		Stderr:    stderr != nil,
		TTY:       t.Raw,
	}, legacyscheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutorForTransports(transport, upgrader, "POST", execRequest.URL())
	if err != nil {
		return err
	}

	return t.Safe(func() error {
		return exec.Stream(streamOptions)
	})
}

// ExecStream executes a command and streams the output to the given streams
func ExecStream(restConfig *rest.Config, pod *k8sv1.Pod, container string, command []string, tty bool, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	wrapper, upgradeRoundTripper, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return err
	}

	return ExecStreamWithTransport(wrapper, upgradeRoundTripper, client, pod, container, command, tty, stdin, stdout, stderr)
}

// ExecBuffered executes a command for kubernetes and returns the output and error buffers
func ExecBuffered(restConfig *rest.Config, pod *k8sv1.Pod, container string, command []string, input io.Reader) ([]byte, []byte, error) {
	stdoutOutput, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, nil, errors.Wrap(err, "create temp file")
	}
	defer os.Remove(stdoutOutput.Name())

	stderrOutput, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, nil, errors.Wrap(err, "create temp file")
	}
	defer os.Remove(stderrOutput.Name())

	kubeExecError := ExecStream(restConfig, pod, container, command, false, input, stdoutOutput, stderrOutput)
	if kubeExecError != nil {
		if _, ok := kubeExecError.(kubectlExec.CodeExitError); ok == false {
			return nil, nil, kubeExecError
		}
	}

	stdoutOutput.Close()
	stderrOutput.Close()

	stdoutBuffer := &bytes.Buffer{}
	stderrBuffer := &bytes.Buffer{}

	// Open stdout
	stdoutOutput, err = os.Open(stdoutOutput.Name())
	if err != nil {
		return nil, nil, errors.Wrap(err, "open stdout file")
	}

	_, err = stdoutBuffer.ReadFrom(stdoutOutput)
	if err != nil {
		return nil, nil, err
	}

	// Open stderr
	stderrOutput, err = os.Open(stdoutOutput.Name())
	if err != nil {
		return nil, nil, errors.Wrap(err, "open stderr file")
	}

	_, err = stderrBuffer.ReadFrom(stdoutOutput)
	if err != nil {
		return nil, nil, err
	}

	return stdoutBuffer.Bytes(), stderrBuffer.Bytes(), kubeExecError
}
