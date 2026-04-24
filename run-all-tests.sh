#!/bin/bash -ex
time ./test-all.sh 2>&1 | tee test-failures.log
time ./scripts/test-integration.sh -short 2>&1 | tee short-test-failures.log
time ./scripts/test-integration.sh 2>&1 | tee full-test-failures.log
