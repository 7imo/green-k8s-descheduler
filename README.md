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