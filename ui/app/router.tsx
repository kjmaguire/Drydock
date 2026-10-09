// The SPA router (remix/router + remix/spa): route actions render screens, and every response goes through the render
// transform that wraps it in the services provider and the shell.

import { createRouter, type RouterContext } from 'remix/router';
import { render } from 'remix/spa';
import { controller } from './actions/controller.tsx';
import { routes } from './routes.ts';
import { NotFound } from './shell/not-found.tsx';
import { Shell } from './shell/shell.tsx';
import { Services, type AppServices } from './state/services.tsx';

export function createAppRouter(services: AppServices) {
  const router = createRouter({
    middleware: [
      render((content, { url }) => (
        <Services value={services}>
          <Shell url={url}>{content}</Shell>
        </Services>
      )),
    ],
    defaultHandler({ render, url }) {
      return render(<NotFound path={url.pathname} />, { status: 404 });
    },
  });
  router.map(routes, controller);
  return router;
}

export type AppRouter = ReturnType<typeof createAppRouter>;
export type AppContext = RouterContext<AppRouter>;

declare module 'remix' {
  interface RouterTypes {
    context: AppContext;
  }
}
