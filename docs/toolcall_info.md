# G‑LM Tool Server Integration (External Go Client / Model)

This README describes how an external model/client (written in Go, running off this VPS) communicates with the G‑LM Tool Server running on this VPS to execute tools (web search, URL fetch, HTTP proxy, vector retrieval, sandboxed code execution). This tool server is exposed on the same domain as OpenWebUI, but only specific paths belong to the tool server.

---

1) Live Service URLs (VPS)

  Public base URL (Tool Server + OpenWebUI share the domain)

   - Base: https://chat.thynaptic.com

  Tool Server discovery (no auth required)

https://chat.thynaptic.com/openapi.json
https://chat.thynaptic.com/docs
https://chat.thynaptic.com/healthz
https://chat.thynaptic.com/version

  Tool execution endpoints (auth required)

  All tool calls are POST JSON to:

   - https://chat.thynaptic.com/tools/web_search
   - https://chat.thynaptic.com/tools/fetch_url
   - https://chat.thynaptic.com/tools/http_request
   - https://chat.thynaptic.com/tools/vector_retrieve
   - https://chat.thynaptic.com/tools/code_exec_sandbox

   Everything else on https://chat.thynaptic.com/ is OpenWebUI. Your integration should only rely on the endpoints above.

---

  2) Contract / Specs (Authoritative References)

  Tool Server API contract

Use this to generate clients, validate schemas, and keep your tool definitions in sync.

  Tool calling spec (if you want the model to decide when to call tools)

  If your external model uses an OpenAI-compatible “tool/function calling” interface (including Ollama’s OpenAI compatibility), these are the specs:

https://platform.openai.com/docs/guides/function-calling (https://platform.openai.com/docs/guides/function-calling)
https://github.com/ollama/ollama/blob/main/docs/openai.md (https://github.com/ollama/ollama/blob/main/docs/openai.md)

  Important: You do not need Ollama to call this tool server. Tool calling is just a convenient orchestration pattern; you can directly call the endpoints yourself.

---

  3) Authentication (Mandatory for /tools/*)

  Every request to https://chat.thynaptic.com/tools/* must include:

   - Authorization: Bearer <TOOLSERVER_API_KEY>
   - X-Client-Id: <TOOLSERVER_CLIENT_ID>
   - Content-Type: application/json
   - Accept: application/json

  Notes

   - Requests missing X-Client-Id or the Bearer token will be rejected (typically 401).
   - /openapi.json, /docs, /healthz, /version are intentionally public so off-VPS engineers can inspect the contract.

---

  4) Response & Error Format

  Success responses

   - 200 OK with application/json bodies matching the OpenAPI schemas.

  Error responses

   - Returned as application/problem+json (Problem Details style) with fields like:
    - status, title, detail
    - optional errors[] for per-field validation failures

  Exact shapes are in OpenAPI under ErrorModel.

---

  5) Tool Endpoints (What They Do)

  All tools are POST JSON.

  5.1 POST https://chat.thynaptic.com/tools/web_search

  Searches the web via Brave Search (server-side key; clients do not supply it).
  See schema: WebSearchInput → WebSearchOutput in OpenAPI.

  5.2 POST https://chat.thynaptic.com/tools/fetch_url

  Fetches a URL (HTTP-only fetch with server allowlist rules) and returns extracted text.
  See schema: FetchURLInput → FetchURLOutput.

  5.3 POST https://chat.thynaptic.com/tools/http_request

  Performs an allowlist-controlled outbound HTTP request (proxy-like behavior).
  See schema: HTTPRequestInput → HTTPRequestOutput.

  5.4 POST https://chat.thynaptic.com/tools/vector_retrieve

  Retrieves semantically similar text chunks from PocketBase-backed storage.

  Input (per OpenAPI VectorRetrieveInput):

   - query (string, required)
   - namespace (string, required)
   - top_k (int, required)
   - filters (object, optional; best-effort equality filtering)

  Output (per OpenAPI VectorRetrieveOutput):

   - matches[] where each match is:
    - id (string)
    - score (float; cosine similarity)
    - text (string)
    - source (string)
    - metadata (object)

  Storage mapping (current implementation details):

Example: prefix glm, namespace docs → collection glm_docs
   - Record fields are best-effort:
    - embedding vector: embedding or vector (array of numbers)
    - chunk text: text or content or chunk
    - source: source or url or path
    - metadata: metadata object (merged with other record fields)

  Embeddings:

   - The tool server computes the query embedding internally (via its configured embeddings backend). External clients do not send embeddings.

  5.5 POST https://chat.thynaptic.com/tools/code_exec_sandbox

  Runs code in a constrained Docker sandbox (language + code + optional files).
  See schema: CodeExecInput → CodeExecOutput.

---

  6) How an External Go Model Should Use This Server

  You have two supported integration styles:

  Option A — Your model calls tools directly (simplest)

   1. Your model decides it needs a tool (search, fetch, retrieve, etc.).
   2. Your Go service sends an HTTPS request to the appropriate endpoint under:
   https://chat.thynaptic.com/tools/...
   3. Your model consumes the JSON output and continues reasoning.

  This option does not require any OpenAI/Ollama tool-calling protocol—just HTTP.

  Option B — Use OpenAI-style tool calling (recommended for LLM orchestration)

  If your model runtime supports function/tool calling:

   1. Provide the model with tool definitions (names + JSONSchema parameters) derived from:
   https://chat.thynaptic.com/openapi.json
   2. When the model emits a tool call, execute it by calling the matching tool endpoint on this server.
   3. Feed the tool result back to the model as a tool-result message.
   4. Repeat until the model produces a final assistant response.

  This is the same loop described in:

   - https://platform.openai.com/docs/guides/function-calling (https://platform.openai.com/docs/guides/function-calling)

----

  7) Operational Expectations (Important for Production)

  Timeouts

   - Treat tool calls as network operations and apply per-request timeouts with cancellation (Go context.Context).
   - code_exec_sandbox can take longer than simple web requests; handle it accordingly.

  Retries

   - Retry only safe/idempotent calls (typically web_search, fetch_url, vector_retrieve; be careful with http_request if it can mutate state).
   - Avoid retrying code_exec_sandbox unless you explicitly want duplicate executions.

  Tool allowlisting

  Even if using an LLM that can emit arbitrary tool names, your dispatcher should only permit:

   - web_search, fetch_url, http_request, vector_retrieve, code_exec_sandbox

---

  8) Quick “Where do I find the exact JSON fields?”

  Use the OpenAPI spec:

   - https://chat.thynaptic.com/openapi.json

  In particular, these schemas define the canonical shapes:

   - WebSearchInput, WebSearchOutput
   - FetchURLInput, FetchURLOutput
   - HTTPRequestInput, HTTPRequestOutput
   - VectorRetrieveInput, VectorRetrieveOutput, Match
   - CodeExecInput, CodeExecOutput
   - ErrorModel

---

  9) Related Service (Data Backend)

  PocketBase reference (backend used by vector_retrieve):

   - Base URL: https://pocketbase.thynaptic.com (https://pocketbase.thynaptic.com)
   - Health: https://pocketbase.thynaptic.com/api/health (https://pocketbase.thynaptic.com/api/health)
   - Admin UI: https://pocketbase.thynaptic.com/_/ (https://pocketbase.thynaptic.com/_/)

   External clients do not need PocketBase credentials to use vector_retrieve; they only need access to the tool server.