// Fixtures of internal/scan/rules/drydock/javascript.yaml: `ruleid:` marks a line the rule must find, `ok:` one it must not.
const cp = require('child_process');
const crypto = require('crypto');

function run(input, db, el, name) {
  // ruleid: drydock.js.eval-of-non-literal
  eval(input);
  // ok: drydock.js.eval-of-non-literal
  eval("1 + 1");

  // ruleid: drydock.js.function-constructor-of-non-literal
  const f = new Function('a', input);
  // ok: drydock.js.function-constructor-of-non-literal
  const g = new Function('a', "return a + 1");

  // ruleid: drydock.js.shell-command-built-from-data
  cp.exec(`git log ${name}`);
  // ruleid: drydock.js.shell-command-built-from-data
  cp.execSync("rm -rf " + name);
  // ok: drydock.js.shell-command-built-from-data
  cp.execFile('git', ['log', name]);

  // ruleid: drydock.js.sql-built-from-data
  db.query(`SELECT * FROM users WHERE name = '${name}'`);
  // ruleid: drydock.js.sql-built-from-data
  db.query("SELECT * FROM users WHERE id = " + input);
  // ok: drydock.js.sql-built-from-data
  db.query('SELECT * FROM users WHERE id = $1', [input]);

  // ruleid: drydock.js.inner-html-from-data
  el.innerHTML = input;
  // ok: drydock.js.inner-html-from-data
  el.innerHTML = "<b>fixed</b>";
  // ok: drydock.js.inner-html-from-data
  el.textContent = input;

  // ruleid: drydock.js.tls-certificate-check-off
  const agent = new https.Agent({ keepAlive: true, rejectUnauthorized: false });
  // ruleid: drydock.js.tls-certificate-check-off
  process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
  // ok: drydock.js.tls-certificate-check-off
  const safe = new https.Agent({ rejectUnauthorized: true });

  // ruleid: drydock.js.weak-hash
  crypto.createHash('md5').update(input).digest('hex');
  // ok: drydock.js.weak-hash
  crypto.createHash('sha256').update(input).digest('hex');

  // ruleid: drydock.js.jwt-none-algorithm
  jwt.verify(token, key, { algorithms: ['HS256', 'none'] });
  // ok: drydock.js.jwt-none-algorithm
  jwt.verify(token, key, { algorithms: ['HS256'] });
}
