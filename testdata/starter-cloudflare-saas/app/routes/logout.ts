import { redirect } from "react-router";
import { logoutCookie } from "../../src/lib/auth";
import type { Route } from "./+types/logout";

export function loader() {
  return redirect("/login");
}

export function action({ request }: Route.ActionArgs) {
  return redirect("/login", { headers: { "Set-Cookie": logoutCookie(request) } });
}
