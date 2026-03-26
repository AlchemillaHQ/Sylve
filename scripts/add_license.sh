#!/bin/sh

addlicense -f docs/CODE_LICENSE \
  -ignore 'internal/assets/web-files/**' \
  -ignore 'internal/assets/swagger/**' \
  cmd/** internal/** web/src/lib/**/*.ts web/src/lib/*.ts pkg/utils/** pkg/crypto/** pkg/disk/** pkg/network/**
