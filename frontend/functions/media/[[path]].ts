// Cloudflare Pages Function: forwards this path to the team backend, so the
// browser only talks to our HTTPS site (no mixed content, no CORS).
// Override the target with the BACKEND_URL env var in the Pages settings.
interface Env {
  BACKEND_URL?: string
}

export const onRequest = ({ request, env }: { request: Request; env: Env }) => {
  const url = new URL(request.url)
  const target = new URL(url.pathname + url.search, env.BACKEND_URL || 'https://api.dormespace.wiki')
  return fetch(new Request(target, request))
}
