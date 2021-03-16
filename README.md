# Skavo <sup><sub><sup><sub><sup><sub>(greek for Delve)</sup></sub></sup></sub></sup></sub>
Skavo opens a remote debugging tunnel to a go process in a pod.

<sup>*Only linux containers are supported

To use, first get it (go >= 1.16)
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

Skavo then forwards the localPort (default 34455) to the remote delve port (default 55443) on the pod. 

You can specify the ports with these options
```shell
skavo -podPort=43210 -localPort=54321
```

skavo uses the current context in `~/.kube/config` by default. 
you can specify the context and kubeconfig using `-context` `-kubeconfig`.
```shell
skavo -context debug-cluster -kubeconfig ~/clusters.kubeconfig
```
To ensure debugging will work correctly in delve, build the go binary in the dockerfile,checked out to the appropriate $GOPATH,
and use go install to install the module. There are a myriad of other ways to build a go project, but this was the only
way I found that worked consistently for delve debugging. 

If you choose to not follow the example project's dockerfile layout, you may or may not be able to get debugging to work. 
