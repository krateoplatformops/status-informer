# status informer

````
helm repo add krateo https://charts.krateo.io
helm repo update krateo
helm install status-informer krateo/status-informer --namespace krateo-system --create-namespace
```