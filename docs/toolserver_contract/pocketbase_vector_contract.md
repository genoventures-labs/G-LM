# PocketBase Vector Retrieval Contract (V1)

This contract freezes the server-side behavior of `POST /tools/vector_retrieve`.

## Namespace and Collection Mapping

- Input `namespace` must match: `^[a-z0-9][a-z0-9_-]{1,63}$`
- Collection name is deterministic:
  - `<VECTOR_COLLECTION_PREFIX>_<namespace>`
  - Example: prefix `glm`, namespace `docs` => `glm_docs`

## Record Shape (Canonical)

Per chunk record:

- `embedding` (required): JSON array of float64
- `text` (required): string
- `source` (required): string
- `metadata` (optional): JSON object, default `{}`

## Compatibility Aliases (Read Path)

For backwards compatibility the retriever reads the first available field in each group:

- embedding: `embedding` or `vector`
- text: `text` or `content` or `chunk`
- source: `source` or `url` or `path`
- metadata: `metadata` plus merged non-system top-level fields

## Query Contract

Input:

- `query` (required, min length 1)
- `namespace` (required)
- `top_k` (optional/int, clamped 1..50, default 8)
- `filters` (optional object, primitive equality values)

Output:

- `matches[]` each containing:
  - `id` string
  - `score` float64 (cosine similarity)
  - `text` string
  - `source` string
  - `metadata` object

## Retrieval Algorithm (Required)

1. Validate `query`, `namespace`, `top_k`, `filters`.
2. Compute query embedding via configured Ollama OpenAI-compatible embeddings endpoint.
3. Load candidate records from mapped PocketBase collection.
4. Apply best-effort equality filters before similarity scoring.
5. Skip records with invalid vectors or dimensional mismatch.
6. Compute cosine similarity for remaining candidates.
7. Sort by descending score and return top `k`.

## Safety and Limits

- Candidate scan cap per request: `5000`
- If all candidates are invalid or mismatched dimensions, return `200` with empty `matches`
- Never return raw embeddings in output

## Required Metrics/Audit Fields

Each request should capture:

- `namespace`
- `top_k`
- `candidate_count`
- `match_count`
- `latency_ms`
- `trace_id`
- `client_id`
