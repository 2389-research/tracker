# ABOUTME: Base Docker image for SWE-bench instance containers.
# ABOUTME: Contains the agent-runner binary and common system dependencies.

FROM python:3.11-bookworm

RUN apt-get update && apt-get install -y \
    git \
    build-essential \
    curl \
    wget \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir setuptools wheel

COPY agent-runner /usr/local/bin/agent-runner

WORKDIR /workspace
