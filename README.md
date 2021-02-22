# Skavo <sup><sub><sup><sub><sup><sub>(greek for Delve)</sup></sub></sup></sub></sup></sub>
Skavo opens a remote debugging tunnel to a process running go in a pod.

<sup>*Only linux containers are supported

To use, first get it
```shell
go get github.com/narcolepticsnowman/skavo
```

Then run skavo
`skavo`

**Choose Pod Screenshot goes here

You will be walked through finding the process to attach to in your cluster.

If delve is not installed on the pod, it will be installed by skavo. Delve is downloaded on the local computer and
transferred to the pod by skavo. Your pod does not need an internet connection.

Skavo starts delve in remote debugging mode on the pod and attaches to the selected process.
For ideal debugging results, the process would ideally have been compiled with -gcflags="all=-N -l" on Go 1.10 or later,
-gcflags="-N -l" on earlier versions of Go. If you're using a version of go < 1.11, you will not be able to see many 
variables without these flags.

Skavo chooses a random free port on the pod, and the local machine to listen.

You can specify the ports with these options
```shell
skavo --podPort=43210 --localPort=54321
```

skavo uses the current context in `~/.kube/config` by default.
