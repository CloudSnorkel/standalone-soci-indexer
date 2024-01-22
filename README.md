# Standalone SOCI Index Builder

* Build and push SOCI index
* Zero dependencies
  * Does not need Docker running or installed
  * Does not need containerd running or installed
  * Does not need AWS CLI installed
* Easier to use than https://github.com/awslabs/soci-snapshotter
* Based on https://github.com/aws-ia/cfn-ecr-aws-soci-index-builder

## Usage

Download latest version with:

```bash
LATEST_VERSION=`curl -w "%{redirect_url}" -fsS https://github.com/CloudSnorkel/standalone-soci-indexer/releases/latest | grep -oE "[^/]+$"`
curl -fsSL https://github.com/CloudSnorkel/standalone-soci-indexer/releases/download/${LATEST_VERSION}/standalone-soci-indexer_Linux_x86_64.tar.gz | tar xz
```

Use it to pull an image, index it, and push a SOCI snapshot with:

```bash
./standalone-soci-indexer 1234567890.dkr.ecr.us-east-1.amazonaws.com/some-repo:latest
```

The indexer will automatically use the provided environment AWS credentials to login to ECR. If you need to use a different authentication method, you can use the `--auth` flag to specify a different authentication token.:

```bash
./standalone-soci-indexer docker.io/some-repo:latest --auth user:password
```
