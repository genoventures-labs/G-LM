# API Intergration Guide  

> Development guide for Agents and LLMs.

---

## Table of Contents

- [1. API reference](#1-api-reference)
- [2. Endpoints you need for “intelligence”](#2-endpoints-you-need-for-intelligence)
- [3. Auth for other projects](#3-auth-for-other-projects)
- [4. Chat request (minimal)](#4-chat-request-minimal)
- [5. Using the OpenAI SDK in another project](#5-using-the-openai-sdk-in-another-project)
- [6. Docker Compose](#6-docker-compose)
- [7. Docker Run](#7-docker-run)
- [8. Quick API Reference](#8-quick-api-reference)

---

## 1. **API reference**

  It includes:
  - Base URLs (localhost and network)
  - Auth: sign-in (JWT) and API keys 
  - All main endpoints and how to call them
  - Request/response examples and a short “copy-paste” section
  - Notes for using from other projects and with the OpenAI SDK

---

## 2. **Endpoints you need for “intelligence”**



  | Purpose     | Method | Endpoint                                          | Auth   | 
  |-------------|--------|---------------------------------------------------|--------|
  | Login       | POST   | /api/v1/auths/signin                              | No     | 
  | List models | GET    | /api/v1/models or /api/models                     | Bearer | 
  | Chat / LLM  | POST   | /api/v1/chat/completions or /api/chat/completions | Bearer | 
  | Embeddings  | POST   | /api/v1/embeddings or /api/embeddings             | Bearer | 
  | Health      | GET    | /health                                           | No     | 
  | Version     | GET    | /api/version                                      | No     | 

  > From another machine: `http://85.31.233.157:8080` (or a domain if you add one in Caddy).

---

## 3. **Auth for other projects**

  • Use API keys (enable_api_keys is true). Create a key (starts with sk-), then use that as Authorization: Bearer sk-...

---

## 4. **Chat request (minimal)**


  POST /api/v1/chat/completions
  Authorization: Bearer <token>
  Content-Type: application/json
  {"model": "mistral:7b", "messages": [{"role": "user", "content": "Your prompt"}]}

  > Models available on this host: mistral:7b, gemma2:2b, phi3:mini,qwen3:4b, qwen2.5:3b-instruct, llama3.2:1b.

---

## 5. **Using the OpenAI SDK in another project**


  from openai import OpenAI
  client = OpenAI(
      base_url="http://127.0.0.1:8080/api/v1",
      api_key="YOUR_JWT_OR_SK_API_KEY"
  )
  r = client.chat.completions.create(
      model="mistral:7b",
      messages=[{"role": "user", "content": "Hello"}]
  )
  print(r.choices[0].message.content)


----

## Example CURL

```
  curl -s -X POST "http://85.31.233.157:8080/api/v1/chat/completions" \
    -H "Authorization: Bearer $OPENWEBUI_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"model":"mistral:7b","messages":[{"role":"user","content":"Hi"}]}'
```

---

## Example Golang

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	// 1) Get token (same logic as Python)
	token, err := getToken()
	if err != nil {
		fmt.Println("getToken error:", err)
		os.Exit(1)
	}

	// 2) Build request
	url := "http://[IP_ADDRESS]/api/v1/chat/completions"
	payload := map[string]interface{}{
		"model": "mistral:7b",
		"messages": []map[string]string{
			{"role": "user", "content": "Hello"},
		},
	}

	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	// 3) Call API
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("API error:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	// 4) Print response
	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println(string(respBody))
}

// getToken() helper (same logic as Python)
func getToken() (string, error) {
	// ... (copy your getToken() implementation here) ...
	return "", nil // placeholder
}
```

## Example Node.js

```js
const axios = require('axios');

async function getChatCompletion() {
    try {
        const response = await axios.post('http://127.0.0.1:8080/api/v1/chat/completions', {
            model: 'mistral:7b',
            messages: [{ role: 'user', content: 'Hello' }],
        }, {
            headers: {
                'Authorization': `Bearer ${process.env.OPENWEBUI_TOKEN}`,
                'Content-Type': 'application/json',
            },
        });
        console.log(response.data);
    } catch (error) {
        console.error('Error:', error.response?.data || error.message);
    }
}

getChatCompletion();
```

---

### Docker Compose

```yaml
version: '3'
services:
  openwebui:
    image: openwebui/openwebui:latest
    container_name: openwebui
    ports:
      - "8080:8080"
    environment:
      - OPENWEBUI_BASE_URL=http://127.0.0.1:8080
      - OPENWEBUI_API_KEY=sk-656caf1a8c6f4ba2aa8285436fe5a33c
      - DEFAULT_MODEL=llama3.2:1b
    volumes:
      - ./data:/data
    restart: unless-stopped
```

---

### Docker Run

```bash
docker run -d \
  --name openwebui \
  -p 8080:8080 \
  -e OPENWEBUI_BASE_URL=http://127.0.0.1:8080 \
  -e OPENWEBUI_API_KEY=sk-656caf1a8c6f4ba2aa8285436fe5a33c \
  -e DEFAULT_MODEL=llama3.2:1b \
  -v ./data:/data \
  openwebui/openwebui:latest
```

---
### Quick API Reference

```bash
# List models
curl -s -X GET "http://127.0.0.1:8080/api/v1/models" \
    -H "Authorization: Bearer $OPENWEBUI_TOKEN"

# Chat completion
curl -s -X POST "http://127.0.0.1:8080/api/v1/chat/completions" \
    -H "Authorization: Bearer $OPENWEBUI_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"model": "mistral:7b", "messages": [{"role": "user", "content": "Hello"}]}'

# Embeddings
curl -s -X POST "http://127.0.0.1:8080/api/v1/embeddings" \
    -H "Authorization: Bearer $OPENWEBUI_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"model": "mistral:7b", "input": "Hello"}'

# Health
curl -s -X GET "http://127.0.0.1:8080/health"

# Version
curl -s -X GET "http://127.0.0.1:8080/api/version"
```

---

