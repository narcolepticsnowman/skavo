# Skavo <sup><sub><sup><sub><sup><sub>(greek for Delve)</sup></sub></sup></sub></sup></sub>
Skavo opens a remote debugging tunnel to a go process in a pod.

<sup>*Only linux containers are supported

### *Disclaimer
I wouldn't use this in production... I claim no responsibility if this tool breaks something dear to you, or in any other case. 

This tool was built with development environments in mind, some actions it takes could be destructive to a real production system...

If you do choose to run this in production, I hope you said your prayers and have your resume updated. May the force be with you.

# Usage
First, install it (go >= 1.16)
```shell
go install github.com/ncsnw/skavo@latest
```

Then run skavo (assuming you have $GOPATH/bin added to your $PATH)
`skavo`

**Choose Pod Screenshot goes here

You will be walked through finding the process to attach to in your cluster.

If delve is not installed on the pod, it will be installed.

Skavo starts delve in remote debugging mode on the pod and either attaches to the selected process or restarts it using
delve exec. The default behavior is to attach to the process.

After skavo starts delve, delve will remain running until the pod is restarted.

Skavo forwards the localPort (default 34455) to the remote delve port (default 55443) on the pod. 

You can specify the ports with these options
```shell
skavo -podPort=43210 -localPort=54321
```

Skavo uses the current context in `~/.kube/config` by default. 

You can specify the context and kubeconfig using `-context` `-kubeconfig`.
```shell
skavo -context debug-cluster -kubeconfig ~/clusters.kubeconfig
```

## Other Modes
Instead of attaching to an existing process, you can have skavo restart the process, or even configure and relaunch the
pods. 

In the restart mode, (specified with the flag `-restart`), the only change from the default attach mode is that the existing
process is killed, and restarted using `dlv exec`. This allows for debugging startup behavior.

### Note about project layout
To ensure debugging will work correctly in delve, build the go binary in the dockerfile, ensure you have the project 
checked out to the appropriate $GOPATH/src directory, and use go install to install the module in the dockerfile.

There are a myriad of other ways to build a go project, but this method seems to work consistently for delve debugging. 
If your project layout is different from the example, you may or may not be able to get debugging to work. This is
related to how delve works, and I am not sure what the key factors for making it work are at this time... 

