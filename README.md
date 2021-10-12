## Deploy Descheduler
#####  Build Docker Image:
```
GOOS=linux go build -o ./app .

export IMAGE=timokraus/green-k8s-descheduler:latest
docker build . -t "${IMAGE}"
docker push "${IMAGE}"
```

#####  Run extender image
```
kubectl apply -f descheduler.yaml
```

#####  Monitor Logs
kubectl -n kube-system logs deploy/green-k8s-descheduler -c green-k8s-descheduler -f