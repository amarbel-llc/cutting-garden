#! /usr/bin/env bats

# The org WebDAV test server lane (FDR 0026, cutting-garden#307). It pins
# cutting-garden-org-testserver, the hermetic Fastmail Files stand-in that
# the org plugin's traversal lane (#308) builds on. Requests go through
# curl; the fixture is zz-tests_bats/fixtures/org.

setup() {
  load "$(dirname "$BATS_TEST_FILE")/lib/common.bash"
  load "$(dirname "$BATS_TEST_FILE")/lib/org.bash"
  export output
  start_org_server
}

teardown() {
  stop_org_server
}

# bats file_tags=org

function org_webdav_propfind_root_lists_files_and_folders { # @test
  run org_dav PROPFIND "" 1
  assert_success
  assert_output --partial "<D:href>/_inbox.org</D:href>"
  assert_output --partial "<D:href>/projects/</D:href>"
  refute_output --partial "garden.org"
}

function org_webdav_propfind_subfolder { # @test
  run org_dav PROPFIND "projects/" 1
  assert_success
  assert_output --partial "<D:href>/projects/garden.org</D:href>"
}

function org_webdav_get_returns_the_file { # @test
  run org_dav GET "_inbox.org"
  assert_success
  assert_line --index 0 "#+FILETAGS: :zz-inbox:"
}

function org_webdav_requires_auth { # @test
  run curl -s -o /dev/null -w '%{http_code}' "${ORG_WEBDAV_URL}_inbox.org"
  assert_success
  assert_output "401"
}
