package manifest

import "testing"

func TestBaseImageWebURL(t *testing.T) {
	cases := map[string]string{
		"FROM docker.io/prplanit/static-site:v0.0.2": "https://hub.docker.com/r/prplanit/static-site",
		"FROM alpine:3.23.5":                         "https://hub.docker.com/_/alpine",
		"FROM docker.io/library/nginx:1.27":          "https://hub.docker.com/_/nginx",
		"FROM ghcr.io/prplanit/static-site:v1":       "https://github.com/prplanit/static-site/pkgs/container/static-site",
		"FROM quay.io/prometheus/node-exporter:v1.8": "https://quay.io/repository/prometheus/node-exporter",
		"FROM cr.pcfae.com/prplanit/thing:latest":    "", // private registry → no public link
		"FROM --platform=linux/amd64 alpine:3.23":    "https://hub.docker.com/_/alpine",
		"FROM golang:1.26-alpine AS build":           "https://hub.docker.com/_/golang",
		"FROM prplanit/static-site@sha256:abc":       "https://hub.docker.com/r/prplanit/static-site",
		"FROM scratch":                               "",
	}
	for ref, want := range cases {
		if got := baseImageWebURL(ref); got != want {
			t.Errorf("baseImageWebURL(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestInventoryItemLink(t *testing.T) {
	cases := []struct {
		item map[string]interface{}
		want string
	}{
		{map[string]interface{}{"manager": "base", "source_ref": "FROM docker.io/prplanit/static-site:v0.0.2"}, "https://hub.docker.com/r/prplanit/static-site"},
		{map[string]interface{}{"manager": "apk", "name": "nginx"}, "https://pkgs.alpinelinux.org/packages?name=nginx"},
		{map[string]interface{}{"manager": "pip", "name": "requests"}, "https://pypi.org/project/requests/"},
		{map[string]interface{}{"manager": "npm", "name": "@scope/pkg"}, "https://www.npmjs.com/package/@scope/pkg"},
		{map[string]interface{}{"manager": "go", "name": "github.com/foo/bar"}, "https://pkg.go.dev/github.com/foo/bar"},
		{map[string]interface{}{"manager": "galaxy", "name": "community.docker"}, "https://galaxy.ansible.com/ui/repo/published/community/docker/"},
		{map[string]interface{}{"manager": "binary", "url": "https://example.com/x"}, "https://example.com/x"},
		{map[string]interface{}{"manager": "apk", "name": ""}, ""},
	}
	for _, c := range cases {
		if got := inventoryItemLink(c.item); got != c.want {
			t.Errorf("inventoryItemLink(%v) = %q, want %q", c.item, got, c.want)
		}
	}
}

func TestRenderBadgesLinked(t *testing.T) {
	data := []interface{}{
		map[string]interface{}{"name": "static-site", "version": "v0.0.2", "manager": "base", "source_ref": "FROM docker.io/prplanit/static-site:v0.0.2"},
	}
	got, err := RenderBadges(data)
	if err != nil {
		t.Fatal(err)
	}
	want := "[![static-site v0.0.2](https://img.shields.io/badge/static--site-v0.0.2-0078D4?style=flat)](https://hub.docker.com/r/prplanit/static-site)"
	if got != want {
		t.Errorf("RenderBadges linked =\n  %q\nwant\n  %q", got, want)
	}
}
