# Sylve LLM / AI Contribution Policy

_Note: This policy is taken from the LLM/AI contribution policies of [Daemonless](https://github.com/daemonless/daemonless/blob/main/LLM_POLICY.md), which in-turn was loosely based on [Jellyfin](https://jellyfin.org/docs/general/contributing/llm-policies/) and [Podman](https://github.com/containers/podman/blob/main/LLM_POLICY.md) policies._

We’re all developers here. We know that AI tools like GitHub Copilot, ChatGPT, and Claude are incredibly useful for writing boilerplate, generating ideas, and speeding up workflows. We aren't going to ban the tools that make you productive.

However, as maintainers, our most valuable resource is time. This policy exists to protect the quality of the `Sylve` codebase and prevent maintainer burnout from reviewing low-effort, AI-generated noise.

This policy applies to **all repositories** under the AlchemillaHQ organization.

## 1. Code Contributions: Own Your Code

You are welcome to use AI tools to help write code or configurations for Sylve, but **you are 100% responsible for what you submit.**

- **Understand what you are committing:** Please do not blindly copy-paste AI output. If you submit a PR, we expect you to understand exactly how the configuration or code works, how it interacts with the system, and why it is necessary.
- **Test it first:** AI-assisted contributions must actually work. If you submit raw, untested AI output and rely on the maintainers to debug it for you, your PR will be closed. Take the time to verify your work locally.
- **Keep it clean:** Remove any leftover AI artifacts, strange naming conventions, or overly verbose comments before you open the PR. Keep things minimal and declarative.

## 2. Communication: Keep It Human

While we allow AI to help write code, **we do not allow AI-generated text in our communication channels.** This includes:

- GitHub Issues and Bug Reports
- Pull Request descriptions and review comments
- Discord/Community discussions

AI tends to pad its responses with corporate fluff, apologies, and irrelevant context. We want clear, direct technical communication. Tell us what is broken or what you are fixing in your own words.

_Exception: If English is not your first language, you are absolutely welcome to use an LLM to translate your original thoughts into English. Just give us a heads-up (e.g., "Translated with AI")._
