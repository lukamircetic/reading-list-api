# Reading List Server

Simple reading list server/database using Go and sqlite for keeping track of articles you have read. Uses Gemini API for extracting article details and creating a short summary. Main use case for me is to show on my personal website what I've been reading.

## Getting Started

Use the below makefile commands to run the project locally.

## MakeFile

Run build make command with tests
```bash
make all
```

Build the application
```bash
make build
```

Run the application
```bash
make run
```

Live reload the application:
```bash
make watch
```

Clean up binary from the last build:
```bash
make clean
```

## OpenRouter API Key (DeepSeek)

This project uses OpenRouter’s OpenAI-compatible API to call the `deepseek/deepseek-r1-0528:free` model (hardcoded).

- Create an OpenRouter API key and set it in your env:
  - `OPENROUTER_API_KEY`
See OpenRouter docs: [`Using the OpenRouter API directly`](https://openrouter.ai/docs/quickstart#using-the-openrouter-api-directly).
Use the provided env example file (`env.example`) to know what variables to set (copy it to `.env`).

