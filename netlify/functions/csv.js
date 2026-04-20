const ALLOWED_HOSTS = [
  'docs.google.com',
  'spreadsheets.google.com',
  'googleusercontent.com',
];

exports.handler = async (event) => {
  const raw = event.queryStringParameters?.url;
  if (!raw) return { statusCode: 400, body: 'missing url parameter' };

  let target;
  try {
    target = new URL(raw);
  } catch {
    return { statusCode: 400, body: 'invalid url' };
  }
  if (target.protocol !== 'https:' && target.protocol !== 'http:') {
    return { statusCode: 400, body: 'invalid url' };
  }
  const host = target.hostname;
  const allowed = ALLOWED_HOSTS.some(h => host === h || host.endsWith('.' + h));
  if (!allowed) {
    return { statusCode: 400, body: 'host not allowed' };
  }

  let upstream;
  try {
    upstream = await fetch(target.toString());
  } catch (e) {
    return { statusCode: 502, body: 'fetch failed: ' + e.message };
  }
  if (!upstream.ok) {
    return { statusCode: 502, body: `upstream returned HTTP ${upstream.status}` };
  }

  const body = await upstream.text();
  return {
    statusCode: 200,
    headers: {
      'content-type': upstream.headers.get('content-type') || 'application/octet-stream',
      'access-control-allow-origin': '*',
    },
    body,
  };
};
