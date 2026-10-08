import { data, Form, redirect } from "react-router";
import { authenticate, devLoginEnabled, loginWithEmail } from "../../src/lib/auth";
import type { Route } from "./+types/login";

export function meta() {
  return [{ title: "Sign in | Starter SaaS" }];
}

export async function loader({ request, context }: Route.LoaderArgs) {
  const { env } = context.cloudflare;
  if (await authenticate(request, env)) throw redirect("/");
  return { devLogin: devLoginEnabled(env) };
}

export async function action({ request, context }: Route.ActionArgs) {
  const form = await request.formData();
  const result = await loginWithEmail(context.cloudflare.env, request, form.get("email"));
  if (!result.ok) return data({ error: result.message }, { status: result.status });
  return redirect("/", { headers: { "Set-Cookie": result.setCookie } });
}

export default function Login({ loaderData, actionData }: Route.ComponentProps) {
  return (
    <main>
      <h1>Sign in</h1>
      {loaderData.devLogin ? (
        <>
          <p className="stub">Stub auth: dev login accepts any email. It is disabled unless ALLOW_DEV_LOGIN is set.</p>
          <Form method="post" className="row">
            <input type="email" name="email" placeholder="you@example.com" aria-label="Email" required />
            <button type="submit" className="primary">
              Sign in
            </button>
          </Form>
          {actionData?.error ? (
            <p className="error" role="alert">
              {actionData.error}
            </p>
          ) : null}
        </>
      ) : (
        <p>Sign-in is not configured for this environment. A real identity provider has to be wired in.</p>
      )}
    </main>
  );
}
