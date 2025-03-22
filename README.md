# Project reading-list-api

Simple reading list server for keeping track of articles you have read. Uses Gemini API for extracting article details and creating a short summary. Main use case for me is to show on my personal website what I've been reading.

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

## Gemini API Key

This project uses the `gemini-2.0-flash` model with Google Search. The free version of the API has 1500 free requests per day which is plenty for a reading list. Get your key [here](https://ai.google.dev/gemini-api/docs/api-key) and then add it to your `.env` file. Use the provided [.env.template](./.env.template) to know what variables to set.

