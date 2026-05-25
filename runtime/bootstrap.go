package runtime

// JSBootstrap contém o código JavaScript de polyfill para as APIs Web Standard
// (Headers, Request, Response, fetch) compatíveis com o modelo de programação
// do Cloudflare Workers. É injetado automaticamente em cada VM Goja do pool
// ANTES do script do utilizador ser carregado.
const JSBootstrap = `
class Headers {
  constructor(init) {
    this._map = new Map();
    this._names = {}; // Mapa para preservar a capitalização original dos headers
    if (init) {
      if (init instanceof Headers) {
        init.forEach(function(value, name) { this.append(name, value); }.bind(this));
      } else if (Array.isArray(init)) {
        for (var i = 0; i < init.length; i++) {
          this.append(init[i][0], init[i][1]);
        }
      } else if (typeof init === 'object' && init !== null) {
        for (var name in init) {
          this.append(name, init[name]);
        }
      }
    }

    // Retorna um Proxy para permitir acesso direto a propriedades como se fosse um map!
    return new Proxy(this, {
      get: function(target, prop) {
        if (typeof prop === 'string') {
          if (prop in target || prop.startsWith('_')) {
            var val = target[prop];
            if (typeof val === 'function') {
              return val.bind(target);
            }
            return val;
          }
          return target.get(prop);
        }
        return target[prop];
      },
      set: function(target, prop, value) {
        if (typeof prop === 'string') {
          if (prop in target || prop.startsWith('_')) {
            target[prop] = value;
            return true;
          }
          target.set(prop, value);
          return true;
        }
        target[prop] = value;
        return true;
      },
      deleteProperty: function(target, prop) {
        if (typeof prop === 'string') {
          if (prop in target || prop.startsWith('_')) {
            delete target[prop];
            return true;
          }
          target.delete(prop);
          return true;
        }
        delete target[prop];
        return true;
      }
    });
  }
  append(name, value) {
    var key = name.toLowerCase();
    if (!this._names[key]) {
      this._names[key] = name;
    }
    var current = this._map.get(key) || [];
    current.push(String(value));
    this._map.set(key, current);
  }
  delete(name) {
    var key = name.toLowerCase();
    this._map.delete(key);
    delete this._names[key];
  }
  get(name) {
    var key = name.toLowerCase();
    var val = this._map.get(key);
    return val ? val.join(', ') : null;
  }
  has(name) {
    return this._map.has(name.toLowerCase());
  }
  set(name, value) {
    var key = name.toLowerCase();
    this._names[key] = name;
    this._map.set(key, [String(value)]);
  }
  forEach(callback) {
    var self = this;
    this._map.forEach(function(values, key) {
      var originalName = self._names[key] || key;
      callback(values.join(', '), originalName);
    });
  }
  entries() {
    var result = [];
    var self = this;
    this._map.forEach(function(values, key) {
      var originalName = self._names[key] || key;
      result.push([originalName, values.join(', ')]);
    });
    return result;
  }
  toJSON() {
    var obj = {};
    var self = this;
    this._map.forEach(function(values, key) {
      var originalName = self._names[key] || key;
      var valStr = values.join(', ');
      obj[originalName] = valStr;
      if (originalName !== key) {
        obj[key] = valStr;
      }
    });
    return obj;
  }
}

class Request {
  constructor(input, options) {
    options = options || {};
    if (input instanceof Request) {
      this.url = input.url;
      this.method = input.method;
      this.headers = new Headers(input.headers);
      this.body = input.body;
    } else {
      this.url = input || '';
      this.method = options.method || 'GET';
      this.headers = new Headers(options.headers);
      this.body = options.body || '';
    }
  }
}

class Response {
  constructor(body, options) {
    options = options || {};
    this.body = (body === undefined || body === null) ? '' : String(body);
    this.status = options.status || 200;
    this.headers = new Headers(options.headers);
  }
}

// Ponte fetch baseada em Go _goFetch nativo (se disponível)
function fetch(input, options) {
  options = options || {};
  var req = new Request(input, options);
  var headersObj = req.headers.toJSON();
  var resData = _goFetch(req.method, req.url, JSON.stringify(headersObj), req.body);
  return new Response(resData.body, {
    status: resData.status,
    headers: resData.headers
  });
}

// _wrapOnTraffic: Wrapper que converte o rawReq bruto (struct Go) num Request
// Web Standard antes de chamar onTraffic, e converte o resultado de volta.
function _wrapOnTraffic(rawReq) {
  if (typeof onTraffic !== 'function') {
    return rawReq;
  }

  // Converte o objeto bruto Go -> classe Request Web Standard
  var req = new Request(rawReq.url, {
    method: rawReq.method,
    headers: rawReq.headers,
    body: rawReq.body
  });
  // Preserva o path do request original
  req.path = rawReq.path;

  var result = onTraffic(req);

  // Se o utilizador retornar um Response direto (ex: WAF de bloqueio)
  if (result instanceof Response) {
    return {
      _isResponse: true,
      status: result.status,
      headers: result.headers.toJSON(),
      body: result.body
    };
  }

  // Se o utilizador retornar um Request (modificado)
  if (result instanceof Request) {
    return {
      _isResponse: false,
      method: result.method,
      url: result.url,
      path: result.path || rawReq.path,
      headers: result.headers.toJSON(),
      body: result.body
    };
  }

  // Fallback: retorna o resultado bruto como está
  return result;
}

// _wrapOnResponse: Wrapper que converte o rawResp bruto (struct Go) num Response
// Web Standard antes de chamar onResponse.
function _wrapOnResponse(rawResp) {
  if (typeof onResponse !== 'function') {
    return rawResp;
  }

  var resp = new Response(rawResp.body, {
    status: rawResp.status,
    headers: rawResp.headers
  });

  var result = onResponse(resp);

  if (result instanceof Response) {
    return {
      status: result.status,
      headers: result.headers.toJSON(),
      body: result.body
    };
  }

  return result;
}
`
