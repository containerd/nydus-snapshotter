apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
    - name: nginx
      image: REGISTRY_IP:5000/busybox:nydus-v5-latest
      imagePullPolicy: Always
      command: ["sh", "-c"]
      args:
        - tail -f /dev/null
