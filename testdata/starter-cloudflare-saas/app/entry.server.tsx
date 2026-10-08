import { isbot } from "isbot";
import { renderToReadableStream } from "react-dom/server";
import type { EntryContext } from "react-router";
import { ServerRouter } from "react-router";

export default async function handleRequest(
  request: Request,
  responseStatusCode: number,
  responseHeaders: Headers,
  routerContext: EntryContext,
) {
  let shellRendered = false;
  const body = await renderToReadableStream(<ServerRouter context={routerContext} url={request.url} />, {
    onError(error: unknown) {
      responseStatusCode = 500;
      // Log only after the shell rendered; earlier errors surface via the thrown rejection.
      if (shellRendered) console.error(error);
    },
    signal: request.signal,
  });
  shellRendered = true;

  // Crawlers get the fully rendered document; browsers get streaming.
  const userAgent = request.headers.get("user-agent");
  if (userAgent && isbot(userAgent)) await body.allReady;

  responseHeaders.set("Content-Type", "text/html; charset=utf-8");
  return new Response(body, { headers: responseHeaders, status: responseStatusCode });
}
