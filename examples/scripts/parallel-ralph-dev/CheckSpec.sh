#!/bin/sh
if [ -f .ai/feature-spec.md ]; then
  printf 'spec_exists'
else
  printf 'no_spec'
fi