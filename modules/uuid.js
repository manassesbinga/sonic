// modules/uuid.js — Shared module for Sonic workers using secure randomness.
// Usage: var uuid = require("uuid");

module.exports = {
    v4: function() {
        try {
            // Request 16 bytes of secure entropy from the Go runtime
            var b64 = _goCryptoRand(16);
            
            // Base64 decoding loop inside Goja VM
            var chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
            var lookup = new Uint8Array(256);
            for (var i = 0; i < chars.length; i++) {
                lookup[chars.charCodeAt(i)] = i;
            }
            var bytes = new Uint8Array(16);
            var raw = b64.replace(/=/g, '');
            var ptr = 0;
            for (var i = 0; i < raw.length; i += 4) {
                var b1 = lookup[raw.charCodeAt(i)];
                var b2 = lookup[raw.charCodeAt(i+1)];
                var b3 = lookup[raw.charCodeAt(i+2)];
                var b4 = lookup[raw.charCodeAt(i+3)];
                bytes[ptr++] = (b1 << 2) | (b2 >> 4);
                if (ptr < 16) bytes[ptr++] = ((b2 & 15) << 4) | (b3 >> 2);
                if (ptr < 16) bytes[ptr++] = ((b3 & 3) << 6) | b4;
            }
            
            // Format bytes as UUID v4 (set version 4 and variant 2)
            bytes[6] = (bytes[6] & 0x0f) | 0x40;
            bytes[8] = (bytes[8] & 0x3f) | 0x80;
            
            var hex = [];
            for (var i = 0; i < 16; i++) {
                var h = bytes[i].toString(16);
                if (h.length === 1) h = '0' + h;
                hex.push(h);
            }
            return hex[0] + hex[1] + hex[2] + hex[3] + '-' +
                   hex[4] + hex[5] + '-' +
                   hex[6] + hex[7] + '-' +
                   hex[8] + hex[9] + '-' +
                   hex[10] + hex[11] + hex[12] + hex[13] + hex[14] + hex[15];
        } catch (e) {
            // Fallback to pseudo-random if native crypto bridge is not available
            return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function(c) {
                var r = Math.random() * 16 | 0;
                return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16);
            });
        }
    },
    validate: function(str) {
        return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(str);
    }
};
