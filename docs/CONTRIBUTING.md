# Contributing

Join the Discord group [here](https://discord.gg/bJB826JvXK) for general discussions.

## Guidelines

Sylve is designed to run on FreeBSD and nothing else (for now). Therefore, it is best to develop on FreeBSD. If you are not using FreeBSD, consider running it in a VM. If you need tools that are unavailable on FreeBSD, we recommend using your preferred operating system and mounting the project directory on the FreeBSD VM via SSHFS.

### 1. Code Style and Formatting

- **Backend**: We use the default Go tooling (`gofmt` and `goimports`).
- **Frontend**: We use `prettier` and `eslint`.

Ensure that your text editor or IDE supports these tools and applies formatting automatically.

### 2. How to Start Hacking

1. Fork the repository.
2. Clone the forked repository.
3. Create a new branch.
4. Make your changes.
5. Push the changes to your fork.
6. Create a pull request.
7. Wait for the pull request to be reviewed and merged.

Refer to the root `README.md` for more information on setting up the project.

### 3. Code of Conduct

Please view [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for details on our code of conduct.

### 4. Setup and Environment

- Ensure you have the recommended development environment set up, including the correct versions of dependencies.
- Instructions for setting up the development environment should be documented in the project's README or a separate `CONTRIBUTING.md` file.
- We recommend using VSCode, Neovim or WebStorm (or GoLand) as your IDE/Editor.
- Test on a live device (apart from unit tests) before committing to the repository.

### 5. Branching Strategy

- Use feature branches. Create a new branch for each feature or bugfix.
- Avoid committing directly to the `main` or `master` branch.
- Consider following a branching strategy like Git Flow or GitHub Flow.

### 6. Commit Messages

- Write clear and descriptive commit messages.
- Start with a short summary (50 chars or less), followed by a blank line and a detailed description if necessary.
- Consider following the conventional commit format: `<type>: <description>`

### 7. Testing

- Add tests for new features and ensure existing tests pass.
- Maintain or improve the current code coverage.
- Test across multiple browsers or environments if applicable.

### 8. Coding Standards

- Follow the project's coding standards and style guides.
- Document functions, methods, and classes.
- Ensure code is clear, maintainable, and well-organized.

### 9. Pull Requests (PRs)

- Make PRs small and focused. They should address a single issue or feature.
- Describe the changes in the PR description and reference any related issues.
- Ensure PRs pass all Continuous Integration (CI) checks.
- Request reviews from relevant team members.
- Address review feedback promptly.

### 10. Dependencies

- Regularly check and update project dependencies.
- When adding a new dependency, ensure it's necessary and doesn't add significant bloat to the project.

### 11. Documentation

- Update documentation in tandem with code changes.
- If adding a new feature, provide usage examples and relevant details.

### 12. Issue Reporting

- Check if an issue already exists before creating a new one.
- Provide clear details, steps to reproduce, and expected vs. actual behavior.

### 13. Performance

- Ensure that new features do not degrade the performance of the application.
- Regularly profile and optimize the codebase.

### 14. Accessibility

- Ensure the web application is accessible to all users, including those with disabilities.
- Follow WAI-ARIA guidelines and test with accessibility tools.

### 15. Responsiveness

- Ensure the application looks and functions well on various screen sizes and devices.

### 16. Feedback Loop

- Actively engage with the community and/or customers and address feedback, suggestions, and concerns.
