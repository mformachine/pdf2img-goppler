# pdf2img-goppler


Simple HTTP API for Poppler built in Go.

It keeps the same simple contract as the original Python and Node.js versions:

- `POST` a PDF
- get back `/media/...` image URLs
- `GET` the images
- uploaded PDFs are deleted after processing
- generated images are stored temporarily and cleaned up automatically

## Features

- `POST /pdftocairo`
- `POST /pdftoppm`
- `POST /pdftohtml`
- `POST /pdfinfo`
- `POST /pdftotext`
- `GET /media/...`
- `GET /healthz`
- `format=png|jpeg|jpg`
- `dpi=<positive integer>`
- automatic media cleanup with TTL
- Docker and Kubernetes friendly

## Docker Hub

```bash
docker pull mformachine/poppler-go
docker run --rm -p 5000:5000 mformachine/poppler-go
```

Versioned image example:

```bash
docker pull mformachine/poppler-go:1.0.0
docker run --rm -p 5000:5000 mformachine/poppler-go:1.0.0
```

## Build locally

```bash
docker build -t mformachine/poppler-go .
docker run --rm -p 5000:5000 mformachine/poppler-go
```

## docker-compose

```bash
docker compose up --build
```

## API

### Health

```bash
curl http://localhost:5000/healthz
```

### Convert PDF to images with pdftocairo

```bash
curl -X POST http://localhost:5000/pdftocairo \
  -F "file=@sample.pdf" \
  -F "format=png" \
  -F "dpi=300"
```

Example response:

```json
{
  "images": [
    "/media/4f4f6fa4cde6413b55af11f83fe278b0/output-1.png",
    "/media/4f4f6fa4cde6413b55af11f83fe278b0/output-2.png"
  ]
}
```

Then fetch an image:

```bash
curl http://localhost:5000/media/4f4f6fa4cde6413b55af11f83fe278b0/output-1.png --output page-1.png
```

### Convert with pdftoppm

```bash
curl -X POST http://localhost:5000/pdftoppm \
  -F "file=@sample.pdf" \
  -F "format=jpeg" \
  -F "dpi=200"
```

### Extract HTML

```bash
curl -X POST http://localhost:5000/pdftohtml -F "file=@sample.pdf"
```

### Extract info

```bash
curl -X POST http://localhost:5000/pdfinfo -F "file=@sample.pdf"
```

### Extract text

```bash
curl -X POST http://localhost:5000/pdftotext -F "file=@sample.pdf"
```

## Environment variables

- `PORT` default: `5000`
- `TMP_DIR` default: `/tmp`
- `MAX_FILE_SIZE_MB` default: `50`
- `MEDIA_TTL_MINUTES` default: `60`
- `CLEANUP_INTERVAL_MINUTES` default: `60`

## Data retention

- uploaded PDFs are removed after processing
- generated files under `/media` are temporary
- media folders older than the TTL are deleted automatically

This keeps the original POST → GET flow while limiting how long converted data stays on disk.

## Kubernetes note

If deployed inside Kubernetes and exposed with a Service named `pdf2img-poppler`, use:

```text
http://pdf2img-poppler:5000/pdftocairo
```

## GitHub Actions publish

This repo includes a workflow that builds and pushes Docker images to Docker Hub:

- `latest` on pushes to `main`
- semver tags on releases like `v1.0.0`

Set these repository secrets:

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

## License

MIT
