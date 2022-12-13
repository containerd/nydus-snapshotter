apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
    - name: busybox
      image: REGISTRY_IP:5000/busybox:nydus-v6-latest
      imagePullPolicy: Always
      command: ["sh", "-c"]
      args:
        - tail -f /dev/null
