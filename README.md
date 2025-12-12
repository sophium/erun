# ERun

Multi tenant, Multi-environment deployment tool.

ERun implements an opinionated way of how to maintain large scale enterprise project.

The problem ERun is trying to solve is "how do you use helm, terraform and cloud provider specific CLI tools in multi environment situation".

The problem escalates to mundane silly everyday tasks and challenges, such as:
- How do I version my project
- How do I increase project version when I build a next version
- How do I deploy my Javascript Single Page Application to CDN
- How do I deploy, develop and maintain my Serverless javascript lambda
- How do I deploy this service only in DEV but not in PROD
- How do I do hot-fixes in PROD without deploying all the mess that is already in develop branch

ERun tries to help solving these issues without getting into the developers way. ERun is entirely optional, all the heavy lifting is done via terraform, helm and git. One can stop using ERun at any point and just navigate and build with these tools.

# How to install development environment

## Install GO

```
brew install go
```

## Install linter


```
brew install golangci-lint
```

## Enable DEBUG on Mac (you may not need it if debug works for you)

```
sudo DevToolsSecurity --enable
xcode-select -p
sudo dseditgroup -o edit -t user -a "$USER" _developer
```

# Contributing

When submitting Pull Request try to adhere to best practices described in https://go.dev/doc/effective_go

# Design

ERun is a developer tool.
I assumes that bunch of core dependencies such as:
- git
- terraform
- helm
- docker
- your cloud provider tools (such as aws utility for Amazon Web Services)

are deployed.

ERun heavily relies on host machine being able to run basic shell (zsh,bash on Mac,Linux or Git bash on windows).

Once deployed ERun will create docker devops CLI shell, that will be OS agnostic, but it will still rely on basic shell capability to be available.

# Configuration

ERun configuration is stored in $HOME/.erun folder and the CLI exposes a `--config` flag
that points to this folder.

# Running

To initialize ERun support for the project, just run `erun` in any project directory. ERun will try to locate .git directory, and then configuration/tenant for this directory in $HOME/.erun directory.