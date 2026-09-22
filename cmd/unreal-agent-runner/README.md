# unreal-agent-runner

Run an AI agent from a prompt or JSON request. It writes events to stdout as
JSONL and exits when the task finishes.

Install with Go 1.27+:

```sh
go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-runner@latest
```

Set an OpenAI API key and run a prompt in the current directory:

```sh
export OPENAI_API_KEY="..."
unreal-agent-runner -p 'Inspect this project and explain how to run its tests.'
```

Or run from source at the repository root:

```sh
go run ./cmd/unreal-agent-runner -p 'Inspect this project and explain how to run its tests.'
```

Choose a workspace and save the output:

```sh
unreal-agent-runner -workspace ./my-project -p 'Summarize this project.' > run.jsonl
```

You can also pass a JSON request as an argument or through stdin:

```sh
unreal-agent-runner '{"prompt":"Summarize this project."}'
unreal-agent-runner < request.json
```

OpenAI is the default provider. Set `UNREAL_HARNESS_LLM_PROVIDER` to `openai`,
`openai-codex`, `openrouter`, `fireworks`, or `ollama`, and
`UNREAL_HARNESS_LLM_MODEL` to choose a model.

Run `unreal-agent-runner -h` for options and the JSON request fields.

## Docker

The `unrea1labs/unreal-agent` image supports Linux on AMD64 and ARM64. Run it
with a project mounted as the workspace:

```sh
docker run --rm -i --user "$(id -u):$(id -g)" \
  -e OPENAI_API_KEY -v "$PWD:/workspace" \
  unrea1labs/unreal-agent:latest -p 'Summarize this project.'
```

Each release also publishes its Git tag (for example, `v0.1.0`) for version pinning.

### Publishing

The existing release workflow tests both image platforms and publishes the
release tag and `latest` before creating the GitHub release. Before the first
publish, have an organization owner create the public `unrea1labs/unreal-agent`
repository on Docker Hub and grant the publishing identity push access.
Configure the GitHub repository variable `DOCKERHUB_USERNAME` and secret
`DOCKERHUB_TOKEN` with that identity and a read/write access token. For an
organization access token (requires Docker Team or Business), use `unrea1labs`
as the username.

To verify publishing locally without creating a release, log in with
`docker login`, then use a separate verification tag:

```sh
image="unrea1labs/unreal-agent:verify-$(git rev-parse --short HEAD)"
docker buildx build --platform linux/amd64,linux/arm64 --push --tag "$image" .
docker buildx imagetools inspect "$image"
for arch in amd64 arm64; do
  docker run --rm --pull always --platform "linux/$arch" "$image" -h
done
```

The local check requires a Buildx builder with support for both target
platforms, such as Docker Desktop. It does not update `latest`.
