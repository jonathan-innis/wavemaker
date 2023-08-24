## Wavemaker

Wavemaker is a tool that generates dummy pods in waves to validate the handling of pod churn on a cluster

### Usage

Run the following command to have wavemaker generate waves with 100 pods, with 100 millicores of cpu and 100 mebibytes of memory each.

```console
./wavemaker --interval 1m --duration 1m --resources cpu=100m,memory=100Mi --count 100
```

### Install on Kubernetes

You can optionally deploy the wavemaker binary as a pod on a Kubernetes cluster to have it run persistently

```console
make apply
```