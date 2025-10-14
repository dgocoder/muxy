# Examples

Example configuration to help you get started with muxy.

## `fullstack.yml`

**Full-stack web app** - React frontend + Node.js backend + PostgreSQL

```bash
muxy examples/fullstack.yml
```

> **Note:** This example uses demo commands that simulate realistic output from a full-stack application. It's for illustrative purposes to show how muxy displays and manages multiple processes. Replace the commands with your actual dev servers (e.g., `npm run dev`, `docker compose up`, etc.) when using muxy in real projects.

Includes:

- 🐘 PostgreSQL database
- 🚀 Node.js API server
- ⚛️ React/Next.js frontend
- 📘 TypeScript type checker (manual start)

Perfect for:

- Understanding muxy's interface
- Learning process orchestration
- Screenshots and demos

## `env-vars.yml`

**Environment variable substitution** - Demonstrates dynamic configuration using environment variables

```bash
# Set some environment variables
export API_PORT=5000
export DB_HOST=postgres.local
export NODE_ENV=production

# Run the example
muxy examples/env-vars.yml
```

Includes:

- 🌍 Dynamic port configuration with `${VAR}` syntax
- 🔧 Default values using `${VAR:-default}` pattern
- 📁 Dynamic directory paths from environment
- 🔗 Composed values (e.g., DATABASE_URL from multiple env vars)

Perfect for:

- Learning environment variable substitution
- Multi-environment configurations (dev/staging/prod)
- Docker and containerized deployments
- CI/CD pipelines

## 🎨 Customizing the Example

The example uses demo commands that simulate real services. To use with real projects:

1. **Change the `command`** to your actual start command:

   ```yaml
   - name: api
     command: npm run dev # Your real command
     directory: ./backend # Your real directory
   ```

2. **Update `directory`** paths to point to your project folders

3. **Set real `environment`** variables your app needs

4. **Adjust `color`** to your preference

## 💡 Tips

- **Copy and modify**: This example is a template - copy it and adapt it to your needs
- **Use colors**: Different colors help distinguish process types (db=blue, api=green, frontend=cyan)
- **Manual start**: Add `autostart: false` for processes you want to start manually

## 🎯 Real-World Usage

To use muxy in your own projects:

1. Copy the example as a starting point:

   ```bash
   cp examples/fullstack.yml ~/myproject/muxy.yml
   ```

2. Edit `muxy.yml` to match your project structure

3. Run muxy from your project directory:
   ```bash
   cd ~/myproject
   muxy  # Uses muxy.yml by default
   ```

## 🤝 Contributing

Have a cool configuration? Submit a PR to add more examples!