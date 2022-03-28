# lambda-clean

![release](https://github.com/rxnew/lambda-clean/actions/workflows/release.yml/badge.svg?branch=release)

AWS Lambda function version cleaner.

## Installation

### Linux and Mac

```shell
curl -L https://github.com/rxnew/lambda-clean/releases/latest/download/lambda-clean-$(uname -s)-$(uname -m).tar.gz | tar -zx
```

## Quick Start

```shell
env AWS_PROFILE=xxx lambda-clean -r us-east-1 my-function
```
