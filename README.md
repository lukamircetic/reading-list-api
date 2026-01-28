# Reading List Server

Simple reading list server/database using Go and sqlite for keeping track of articles you have read. Uses Exa to extract article details and create a short summary. Main use case for me is to show on my personal website what I've been reading.

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

## Exa API Key

This project uses Exa’s API to fetch page content/metadata and produce a short summary.

- Create an Exa API key and set it in your env:
  - `EXA_API_KEY`
Use the provided env example file (`env.example`) to know what variables to set (copy it to `.env`).

