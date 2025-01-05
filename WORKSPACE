load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

http_archive(
    name = "io_bazel_rules_go",
    sha256 = "f4a9314518ca6acfa16cc4ab43b0b8ce1e4ea64b81c38d8a3772883f153346b8",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/rules_go/releases/download/v0.50.1/rules_go-v0.50.1.zip",
        "https://github.com/bazelbuild/rules_go/releases/download/v0.50.1/rules_go-v0.50.1.zip",
    ],
)

http_archive(
    name = "bazel_gazelle",
    integrity = "sha256-t2D3/nUXOIYAf3wuYWohJBII89kOhlfcZdNqdx6Ra2o=",
    urls = [
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.39.1/bazel-gazelle-v0.39.1.tar.gz",
    ],
)

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies")
load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")

############################################################
# Define your own dependencies here using go_repository.
# Else, dependencies declared by rules_go/gazelle will be used.
# The first declaration of an external repository "wins".
############################################################

load("//:deps.bzl", "go_dependencies")

local_repository(
    name = "com_github_findyourpaths_goskyr",
    path = "./",
)

local_repository(
    name = "com_github_findyourpaths_phil",
    path = "../../phil",
)

# gazelle:repository_macro deps.bzl%go_dependencies
go_dependencies()

go_rules_dependencies()

# go_register_toolchains(version = "1.20.5")
go_register_toolchains(version = "host")

gazelle_dependencies()
