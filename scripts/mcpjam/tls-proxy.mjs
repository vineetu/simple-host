// TLS front for the local fixture: MCP OAuth requires https resource URLs.
import https from 'node:https'; import http from 'node:http'; import fs from 'node:fs';
const [listen, target] = [Number(process.env.TLS_PORT || 18443), new URL(process.env.TARGET || 'http://127.0.0.1:18190')];
https.createServer({ key: fs.readFileSync(process.env.TLS_KEY), cert: fs.readFileSync(process.env.TLS_CERT) }, (req, res) => {
  const up = http.request({ host: target.hostname, port: target.port, path: req.url, method: req.method,
    headers: { ...req.headers, 'x-forwarded-proto': 'https' } }, (r) => { res.writeHead(r.statusCode, r.headers); r.pipe(res); });
  up.on('error', () => { res.writeHead(502); res.end(); });
  req.pipe(up);
}).listen(listen, '127.0.0.1');
