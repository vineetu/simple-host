/* Simple Host sign-in card. SHSignIn.mount(container, opts) */
(function () {
  "use strict";
  if (window.SHSignIn) return;

  var MESSAGES = /*sh:signin-messages*/{};
  var seq = 0;
  var GMAIL_WEB = "https://mail.google.com/mail/";
  var GMAIL_ANDROID = "intent://#Intent;scheme=googlegmail;package=com.google.android.gm;S.browser_fallback_url=https%3A%2F%2Fmail.google.com%2Fmail%2F;end";
  var EMAIL_RE = /^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$/;
  var GOOGLE_SVG = '<svg viewBox="0 0 48 48" width="20" height="20" aria-hidden="true"><path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/><path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/><path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/><path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/></svg>';
  var INFO_SVG = '<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 4.2v4.6M8 10.9v.9" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>';

  var INK_CSS = [
    ".sh-si{box-sizing:border-box;width:100%;max-width:400px;background:var(--paper-hi,var(--surface,#fff));border:1.5px solid var(--line-soft,var(--border,#c9c9d1));border-radius:var(--wobble,var(--radius,14px));box-shadow:var(--shadow,none);padding:28px 26px 22px;color:var(--line,var(--text,#1a1a1a));font:16px/1.5 var(--font,system-ui,-apple-system,'Segoe UI',sans-serif)}",
    ".sh-si *,.sh-si *::before,.sh-si *::after{box-sizing:border-box}",
    ".sh-si .sh-si-eyebrow{font:600 12px/1.4 var(--font,system-ui);letter-spacing:.12em;text-transform:uppercase;color:var(--line-faint,var(--dim,#5d5d66));margin:0 0 6px}",
    ".sh-si h1{font-family:var(--hand,var(--font,system-ui));font-size:40px;line-height:1.15;margin:0 0 6px;color:var(--line,var(--text,#1a1a1a));font-weight:700;overflow-wrap:anywhere}",
    ".sh-si .sh-si-lead{margin:0 0 20px;color:var(--line-soft,var(--text-secondary,#5d5d66));font-size:15.5px;line-height:1.5;overflow-wrap:anywhere}",
    ".sh-si .sh-si-lead b{color:var(--line,var(--text,#1a1a1a));font-weight:700}",
    ".sh-si .sh-si-gbtn{display:flex;align-items:center;justify-content:center;gap:10px;width:100%;min-height:44px;height:46px;box-sizing:border-box;border-radius:23px;background:#fff;border:1px solid #747775;color:#1f1f1f;font:500 15px/20px Roboto,system-ui,-apple-system,'Segoe UI',sans-serif!important;letter-spacing:.1px;cursor:pointer;padding:0 12px}",
    "html[data-theme=dark] .sh-si .sh-si-gbtn{background:#131314;border-color:#8e918f;color:#e3e3e3}",
    ".sh-si .sh-si-gbtn svg{width:20px;height:20px;flex:none}",
    ".sh-si .sh-si-obtn{display:flex;align-items:center;justify-content:center;gap:10px;width:100%;min-height:44px;height:46px;box-sizing:border-box;border-radius:23px;background:var(--paper-hi,var(--surface,#fff));border:1px solid var(--line-soft,var(--border,#c9c9d1));color:var(--line,var(--text,#1a1a1a));font:500 15px/20px system-ui,-apple-system,'Segoe UI',sans-serif!important;margin-top:10px;cursor:pointer;padding:0 12px}",
    ".sh-si .sh-si-or{display:flex;align-items:center;gap:12px;margin:18px 0;color:var(--line-faint,var(--dim,#5d5d66));font-size:14px}",
    ".sh-si .sh-si-or::before,.sh-si .sh-si-or::after{content:'';flex:1;border-top:1px solid var(--paper-ink,var(--border,#c9c9d1))}",
    ".sh-si label{display:block;font-family:var(--font,system-ui)!important;font-weight:600;font-size:14px!important;color:var(--line,var(--text,#1a1a1a));margin:0 0 6px}",
    ".sh-si input.sh-si-f{width:100%;max-width:100%;min-width:0;box-sizing:border-box;height:48px;min-height:44px;padding:0 14px;background:var(--paper,var(--bg,#fff));border:1.5px solid var(--line-soft,var(--border,#c9c9d1));border-radius:10px;color:var(--line,var(--text,#1a1a1a));font:16px var(--font,system-ui)}",
    ".sh-si input.sh-si-f:focus{outline:3px solid color-mix(in srgb,var(--accent,#116b77) 35%,transparent);outline-offset:1px;border-color:var(--accent,#116b77)}",
    ".sh-si input.sh-si-code{height:60px;text-align:center;font:600 30px/1 var(--mono,ui-monospace,monospace);letter-spacing:.42em;padding-left:.42em}",
    "@media (max-width:360px){.sh-si input.sh-si-code{font-size:24px;letter-spacing:.28em;padding-left:.28em}}",
    ".sh-si input.sh-si-bad{border-color:var(--error,#a33f31)}",
    ".sh-si .sh-si-pbtn{display:block;width:100%;min-height:44px;height:48px;margin-top:12px;border:0;border-radius:var(--wobble,var(--radius,10px));background:var(--accent,#116b77);color:var(--on-accent,#fff);font-family:var(--note,var(--font,system-ui));font-size:18px;box-shadow:var(--shadow,none);cursor:pointer}",
    ".sh-si .sh-si-pbtn[disabled]{opacity:.55;cursor:default}",
    ".sh-si .sh-si-fine{margin:16px 0 0;font-size:13px;line-height:1.5;color:var(--line-faint,var(--dim,#5d5d66))}",
    ".sh-si .sh-si-row{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-top:14px;font-size:14px;color:var(--line-soft,var(--text-secondary,#5d5d66));flex-wrap:wrap}",
    ".sh-si a,.sh-si .sh-si-link{display:inline-flex;align-items:center;color:var(--accent,#116b77);background:none;border:0;padding:0;font:inherit;cursor:pointer;text-decoration:underline;min-height:44px}",
    ".sh-si .sh-si-muted{color:var(--line-faint,var(--dim,#5d5d66))}",
    ".sh-si .sh-si-err{display:flex;gap:8px;align-items:flex-start;margin:8px 0 0;color:var(--error,#a33f31);font-size:14px;line-height:1.45}",
    ".sh-si .sh-si-err svg{flex:none;width:16px;height:16px;margin-top:2px}",
    ".sh-si .sh-si-warn{margin:16px 0 0;padding:12px 14px;border-radius:10px;border:1.5px solid var(--error,#a33f31);color:var(--line,var(--text,#1a1a1a));font-size:14.5px;line-height:1.5}",
    ".sh-si .sh-si-status{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-top:8px;font-size:14px;flex-wrap:wrap}",
    ".sh-si .sh-si-ok{color:var(--success,#0e7490)}",
    ".sh-si .sh-si-badtxt{color:var(--error,#a33f31)}",
    ".sh-si .sh-si-top{margin:0 0 14px}"
  ].join("");

  var PLAIN_CSS = [
    ":host{display:block;width:100%;max-width:380px;color-scheme:inherit;font:15px/1.5 system-ui,-apple-system,'Segoe UI',sans-serif}",
    ".sh-si{box-sizing:border-box;width:100%;background:light-dark(#fff,#1c1c1f);color:light-dark(#1a1a1a,#f2f2f2);border-radius:var(--sh-radius,14px);padding:24px 22px 18px}",
    ".sh-si *,.sh-si *::before,.sh-si *::after{box-sizing:border-box}",
    ".sh-si .sh-si-eyebrow{font-size:13px;color:light-dark(#5d5d66,#a8a8b0);margin:0 0 4px}",
    ".sh-si h1{font:650 21px/1.25 system-ui;margin:0 0 6px;color:light-dark(#1a1a1a,#f2f2f2)}",
    ".sh-si .sh-si-lead{margin:0 0 16px;color:light-dark(#5d5d66,#a8a8b0);font-size:14.5px;overflow-wrap:anywhere}",
    ".sh-si .sh-si-lead b{color:light-dark(#1a1a1a,#f2f2f2)}",
    ".sh-si .sh-si-gbtn{display:flex;align-items:center;justify-content:center;gap:10px;width:100%;min-height:44px;height:44px;border-radius:22px;background:light-dark(#fff,#131314);border:1px solid light-dark(#747775,#8e918f);color:light-dark(#1f1f1f,#e3e3e3);font:500 15px Roboto,system-ui;cursor:pointer;padding:0 12px}",
    ".sh-si .sh-si-gbtn svg{width:20px;height:20px;flex:none}",
    ".sh-si .sh-si-obtn{display:flex;align-items:center;justify-content:center;width:100%;min-height:44px;height:44px;border-radius:22px;margin-top:10px;background:light-dark(#fff,#1c1c1f);border:1px solid light-dark(#c9c9d1,#47474f);color:light-dark(#1a1a1a,#f2f2f2);font:500 15px system-ui;cursor:pointer}",
    ".sh-si .sh-si-or{display:flex;align-items:center;gap:10px;margin:14px 0;color:light-dark(#5d5d66,#a8a8b0);font-size:13px}",
    ".sh-si .sh-si-or::before,.sh-si .sh-si-or::after{content:'';flex:1;border-top:1px solid light-dark(#c9c9d1,#47474f)}",
    ".sh-si label{display:block;font-weight:600;font-size:13.5px;margin:0 0 5px}",
    ".sh-si input.sh-si-f{width:100%;max-width:100%;min-width:0;height:46px;min-height:44px;border:1.5px solid light-dark(#c9c9d1,#47474f);border-radius:var(--sh-radius,9px);background:light-dark(#fff,#111114);color:light-dark(#1a1a1a,#f2f2f2);padding:0 12px;font:16px system-ui}",
    ".sh-si input.sh-si-code{height:56px;text-align:center;font:600 27px ui-monospace,monospace;letter-spacing:.4em;padding-left:.4em}",
    "@media (max-width:360px){.sh-si input.sh-si-code{font-size:22px;letter-spacing:.24em;padding-left:.24em}}",
    ".sh-si input.sh-si-bad{border-color:light-dark(#b3261e,#ff9a9a)}",
    ".sh-si .sh-si-pbtn{display:block;width:100%;min-height:44px;height:46px;margin-top:10px;border:0;border-radius:var(--sh-radius,9px);background:var(--sh-accent,light-dark(#1a1a1a,#f2f2f2));color:var(--sh-on-accent,light-dark(#fff,#111));font:600 15.5px system-ui;cursor:pointer}",
    ".sh-si .sh-si-pbtn[disabled]{opacity:.55;cursor:default}",
    ".sh-si .sh-si-fine{margin:14px 0 0;font-size:12.5px;color:light-dark(#5d5d66,#a8a8b0)}",
    ".sh-si .sh-si-row{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-top:12px;font-size:13.5px;color:light-dark(#5d5d66,#a8a8b0);flex-wrap:wrap}",
    ".sh-si a,.sh-si .sh-si-link{display:inline-flex;align-items:center;color:inherit;background:none;border:0;padding:0;font:inherit;cursor:pointer;text-decoration:underline;min-height:44px}",
    ".sh-si .sh-si-muted{color:light-dark(#5d5d66,#a8a8b0)}",
    ".sh-si .sh-si-err{display:flex;gap:8px;align-items:flex-start;margin:8px 0 0;color:light-dark(#b3261e,#ff9a9a);font-size:14px;line-height:1.45}",
    ".sh-si .sh-si-err svg{flex:none;width:16px;height:16px;margin-top:2px}",
    ".sh-si .sh-si-warn{margin:16px 0 0;padding:12px 14px;border-radius:10px;border:1.5px solid light-dark(#b3261e,#ff9a9a);font-size:14.5px;line-height:1.5}",
    ".sh-si .sh-si-status{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-top:8px;font-size:14px;flex-wrap:wrap}",
    ".sh-si .sh-si-ok{color:light-dark(#0e7490,#9edacf)}",
    ".sh-si .sh-si-badtxt{color:light-dark(#b3261e,#ff9a9a)}",
    ".sh-si .sh-si-top{margin:0 0 14px}"
  ].join("");

  function isGmail(email) {
    email = String(email || "").trim().toLowerCase();
    var at = email.lastIndexOf("@");
    if (at < 0) return false;
    var d = email.slice(at + 1);
    return d === "gmail.com" || d === "googlemail.com";
  }

  function gmailURL() {
    var ua = (typeof navigator !== "undefined" && navigator.userAgent) || "";
    if (/Android/i.test(ua)) return GMAIL_ANDROID;
    return GMAIL_WEB;
  }

  function digits(s) {
    return String(s || "").replace(/\D/g, "").slice(0, 6);
  }

  function pad2(n) {
    return n < 10 ? "0" + n : String(n);
  }

  function has(arr, name) {
    if (!arr) return false;
    for (var i = 0; i < arr.length; i++) if (arr[i] === name) return true;
    return false;
  }

  function validEmail(s) {
    s = String(s || "").trim();
    return s !== "" && EMAIL_RE.test(s);
  }

  function fillTpl(tpl, vars) {
    var out = String(tpl || "");
    var k;
    for (k in vars) {
      if (!Object.prototype.hasOwnProperty.call(vars, k)) continue;
      out = out.split("{" + k + "}").join(vars[k] == null ? "" : String(vars[k]));
    }
    return out.replace(/\s+/g, " ").replace(/\s+\./g, ".").trim();
  }

  function message(err, product) {
    err = err || {};
    var body = err.body || {};
    var reason = err.reason || body.reason || "";
    var code = err.code || body.code || "";
    var key = reason;
    if (!key) {
      if (code === "signup_unavailable_region" || code === "signup_blocked") key = "signup_blocked";
      else if (code === "invalid_code") key = "code_expired";
      else if (MESSAGES[code]) key = code;
      else if (err.status === 0 || code === "network" || /network|failed to fetch|load failed/i.test(String(err.message || ""))) key = "network";
      else key = "unknown";
    }
    if (key === "account_suspended") {
      return body.error || err.message || MESSAGES.account_suspended || "this account has been suspended by the operator";
    }
    var tpl = MESSAGES[key] || MESSAGES.unknown || "Something went wrong. Try again.";
    var n = err.tries_left;
    if (n == null) n = body.tries_left;
    if (key === "code_wrong") {
      if (n == 1) return "That code is not right. 1 try left.";
      if (n != null && n !== "") return fillTpl(tpl, { n: n });
      return "That code is not right.";
    }
    if (key === "code_expired" && tpl.indexOf("{min}") >= 0 && (err.min == null && body.min == null)) {
      return "That code has expired or was already used. Send a new code.";
    }
    if (body.error && !reason && (key === "unknown" || key === "code_expired") && body.error) {
      return body.error;
    }
    return fillTpl(tpl, {
      n: n,
      min: err.min != null ? err.min : body.min,
      product: product || "Simple Host"
    });
  }

  function el(tag, attrs) {
    var n = document.createElement(tag);
    var k, v;
    if (attrs) {
      for (k in attrs) {
        if (!Object.prototype.hasOwnProperty.call(attrs, k)) continue;
        v = attrs[k];
        if (v == null || v === false) continue;
        if (k === "className") n.className = v;
        else if (k === "text") n.textContent = v;
        else if (k === "html") n.innerHTML = v;
        else n.setAttribute(k, v === true ? "" : String(v));
      }
    }
    var i;
    for (i = 2; i < arguments.length; i++) {
      if (arguments[i] == null) continue;
      if (typeof arguments[i] === "string") n.appendChild(document.createTextNode(arguments[i]));
      else n.appendChild(arguments[i]);
    }
    return n;
  }

  function clear(n) {
    while (n.firstChild) n.removeChild(n.firstChild);
  }

  function ensureInkCSS() {
    if (document.getElementById("sh-signin-css")) return;
    var s = document.createElement("style");
    s.id = "sh-signin-css";
    s.textContent = INK_CSS;
    (document.head || document.documentElement).appendChild(s);
  }

  function mount(container, opts) {
    opts = opts || {};
    var skin = opts.skin === "plain" ? "plain" : "ink";
    var emailOn = opts.emailEnabled !== false;
    var providers = opts.providers || [];
    var product = opts.product || "Simple Host";
    var uid = "sh-si-" + (++seq);
    var step = "start";
    var email = "";
    var busy = false;
    var startMsg = "";
    var fieldErr = "";
    var codeErr = "";
    var codeVal = "";
    var codeReason = "";
    var lastSubmitted = "";
    var inFlight = false;
    var signingIn = false;
    var resendLeft = 0;
    var resendTimer = null;
    var destroyed = false;
    var handleWait = null;
    var handleTimer = null;
    var handleSeq = 0;
    var handleState = null;
    var host, root, shadow;

    if (skin === "plain") {
      host = el("div", { className: "sh-si-host" });
      shadow = host.attachShadow({ mode: "open" });
      shadow.appendChild(el("style", { text: PLAIN_CSS }));
      root = el("div", { className: "sh-si" });
      shadow.appendChild(root);
      container.appendChild(host);
    } else {
      ensureInkCSS();
      host = el("div", { className: "sh-si" });
      root = host;
      container.appendChild(host);
    }

    function stopResend() {
      if (resendTimer) {
        clearInterval(resendTimer);
        resendTimer = null;
      }
    }

    function startResend() {
      stopResend();
      resendLeft = 30;
      resendTimer = setInterval(function () {
        if (destroyed) {
          stopResend();
          return;
        }
        resendLeft -= 1;
        if (resendLeft <= 0) {
          resendLeft = 0;
          stopResend();
        }
        var hold = root.querySelector("#" + uid + "-resend");
        if (hold) updateResend(hold);
      }, 1000);
    }

    function updateResend(hold) {
      clear(hold);
      if (resendLeft > 0) {
        hold.appendChild(el("span", { className: "sh-si-muted", text: "Resend in 0:" + pad2(resendLeft) }));
      } else {
        var b = el("button", { type: "button", className: "sh-si-link", text: "Resend code" });
        b.addEventListener("click", function () { resend(); });
        hold.appendChild(b);
      }
    }

    function focusSoon(id) {
      setTimeout(function () {
        var n = root.querySelector("#" + id);
        if (n && n.focus) {
          try { n.focus(); } catch (e) {}
        }
      }, 0);
    }

    function setBusy(on) {
      busy = !!on;
      var nodes = root.querySelectorAll("button, input, a");
      var i;
      for (i = 0; i < nodes.length; i++) {
        if (nodes[i].getAttribute("data-keep") === "1") continue;
        if (busy) nodes[i].setAttribute("disabled", "");
        else nodes[i].removeAttribute("disabled");
      }
    }

    function rejectErr(e) {
      var err = e;
      if (!err || typeof err !== "object") {
        err = new Error(String(e || "error"));
        err.code = "unknown";
      }
      if (err.status == null && typeof fetch !== "undefined" && e && e.name === "TypeError") {
        err.status = 0;
        err.code = "network";
      }
      return err;
    }

    function applyVerifyFail(err) {
      err = rejectErr(err);
      codeReason = err.reason || (err.body && err.body.reason) || "";
      codeErr = message(err, product);
      lastSubmitted = codeVal;
      signingIn = false;
      inFlight = false;
      render();
      var box = root.querySelector("input.sh-si-code");
      if (box && box.select) {
        try { box.select(); } catch (e2) {}
      }
    }

    function request(emailAddr) {
      if (typeof opts.requestCode !== "function") {
        return Promise.reject(new Error("requestCode missing"));
      }
      return Promise.resolve(opts.requestCode(emailAddr));
    }

    function verify(emailAddr, code) {
      if (typeof opts.verifyCode !== "function") {
        return Promise.reject(new Error("verifyCode missing"));
      }
      return Promise.resolve(opts.verifyCode(emailAddr, code));
    }

    function goStart(msg) {
      step = "start";
      startMsg = msg || "";
      fieldErr = "";
      codeErr = "";
      codeReason = "";
      signingIn = false;
      handleState = null;
      render();
      focusSoon(uid + "-email");
    }

    function goCode(addr) {
      step = "code";
      email = addr || email;
      startMsg = "";
      fieldErr = "";
      codeErr = "";
      codeVal = "";
      codeReason = "";
      lastSubmitted = "";
      signingIn = false;
      startResend();
      render();
      focusSoon(uid + "-code");
    }

    function sendCode(fromResend) {
      if (inFlight) return;
      var addr = String(email || "").trim().toLowerCase();
      if (!validEmail(addr)) {
        fieldErr = message({ code: "invalid_email" }, product);
        render();
        return;
      }
      inFlight = true;
      setBusy(true);
      request(addr).then(function (res) {
        inFlight = false;
        setBusy(false);
        email = (res && res.email) || addr;
        if (fromResend && step === "code") {
          codeErr = "";
          codeReason = "";
          codeVal = "";
          lastSubmitted = "";
          startResend();
          render();
          focusSoon(uid + "-code");
        } else {
          goCode(email);
        }
      }).catch(function (err) {
        inFlight = false;
        setBusy(false);
        err = rejectErr(err);
        if (step === "code" && fromResend) {
          codeErr = message(err, product);
          render();
        } else {
          startMsg = message(err, product);
          step = "start";
          render();
        }
      });
    }

    function submitCode(code) {
      code = digits(code);
      if (code.length !== 6 || inFlight) return;
      if (code === lastSubmitted) return;
      lastSubmitted = code;
      inFlight = true;
      signingIn = true;
      codeErr = "";
      render();
      verify(email, code).then(function () {
        inFlight = false;
        signingIn = true;
        render();
      }).catch(function (err) {
        applyVerifyFail(err);
      });
    }

    function onContinue() {
      var inp = root.querySelector("#" + uid + "-email");
      var addr = inp ? String(inp.value || "").trim() : email;
      email = addr;
      if (!validEmail(addr)) {
        fieldErr = message({ code: "invalid_email" }, product);
        render();
        return;
      }
      fieldErr = "";
      startMsg = "";
      sendCode(false);
    }

    function resend() {
      if (resendLeft > 0 || inFlight) return;
      sendCode(true);
    }

    function sendNew() {
      codeReason = "";
      codeErr = "";
      lastSubmitted = "";
      sendCode(true);
    }

    function onCodeInput(inp) {
      var next = digits(inp.value);
      if (inp.value !== next) inp.value = next;
      codeVal = next;
      if (next.length === 6) submitCode(next);
    }

    function checkHandle(value, token) {
      var h = String(value || "").trim().toLowerCase();
      if (!h) {
        return Promise.resolve({ ok: false, address: handleState && handleState.address || "", message: "" });
      }
      return fetch("/v1/handles/check?handle=" + encodeURIComponent(h), { credentials: "same-origin" })
        .then(function (r) {
          return r.json().then(function (b) { return { status: r.status, b: b }; });
        })
        .then(function (x) {
          if (token !== handleSeq) return null;
          if (x.status === 429) return { ok: true, address: x.b.address || "", message: "" };
          if (x.b && x.b.available) return { ok: true, address: x.b.address || "", message: "Available" };
          return { ok: false, address: (x.b && x.b.address) || "", message: (x.b && x.b.error) || "That address cannot be used." };
        });
    }

    function renderHandle() {
      var hs = handleState || {};
      var sug = hs.suggestion || "";
      var wrap = el("div");
      wrap.appendChild(el("p", { className: "sh-si-eyebrow", text: "New account" }));
      wrap.appendChild(el("h1", { text: "Pick your address" }));
      wrap.appendChild(el("p", { className: "sh-si-lead", text: "Your sites will live under this name. You can change it later, not more than once in 30 days." }));
      wrap.appendChild(el("label", { "for": uid + "-handle", text: "Address" }));
      var inp = el("input", {
        className: "sh-si-f" + (hs.ok === false ? " sh-si-bad" : ""),
        id: uid + "-handle",
        type: "text",
        autocomplete: "off",
        spellcheck: "false",
        autocapitalize: "off"
      });
      inp.value = hs.value != null ? hs.value : sug;
      wrap.appendChild(inp);
      var status = el("div", { className: "sh-si-status" });
      var addrEl = el("span", { className: "sh-si-addr" });
      addrEl.textContent = hs.address || "";
      var flag = el("span", { className: hs.ok ? "sh-si-ok" : "sh-si-badtxt" });
      flag.textContent = hs.ok ? "Available" : (hs.message || "");
      status.appendChild(addrEl);
      status.appendChild(flag);
      wrap.appendChild(status);
      var create = el("button", { type: "button", className: "sh-si-pbtn", text: "Create my account" });
      if (!hs.ok) create.setAttribute("disabled", "");
      create.addEventListener("click", function () {
        if (!handleState || !handleState.ok || !handleWait) return;
        var done = handleWait;
        handleWait = null;
        done(String(inp.value || "").trim().toLowerCase());
      });
      wrap.appendChild(create);
      var cancel = el("button", { type: "button", className: "sh-si-link", text: "Cancel", "data-keep": "1" });
      cancel.style.marginTop = "12px";
      cancel.addEventListener("click", function () {
        if (!handleWait) return;
        var done = handleWait;
        handleWait = null;
        done(null);
      });
      wrap.appendChild(cancel);
      var who = el("p", { className: "sh-si-fine" });
      who.appendChild(document.createTextNode("Signed in as "));
      who.appendChild(el("span", { text: email || "" }));
      who.appendChild(document.createTextNode("."));
      wrap.appendChild(who);

      function schedule() {
        handleState.value = inp.value;
        handleState.ok = false;
        create.setAttribute("disabled", "");
        clearTimeout(handleTimer);
        var token = ++handleSeq;
        handleTimer = setTimeout(function () {
          checkHandle(inp.value, token).then(function (r) {
            if (!r || token !== handleSeq || !handleState) return;
            handleState.ok = r.ok;
            handleState.address = r.address || handleState.address;
            handleState.message = r.message || "";
            handleState.value = inp.value;
            render();
            var again = root.querySelector("#" + uid + "-handle");
            if (again) {
              again.focus();
              try { again.setSelectionRange(again.value.length, again.value.length); } catch (e) {}
            }
          }).catch(function () {});
        }, 350);
      }
      inp.addEventListener("input", schedule);
      return wrap;
    }

    function renderStart() {
      var wrap = el("div");
      if (opts.top) {
        var top = el("div", { className: "sh-si-top" });
        top.appendChild(opts.top);
        wrap.appendChild(top);
      }
      if (opts.eyebrow) wrap.appendChild(el("p", { className: "sh-si-eyebrow", text: opts.eyebrow }));
      wrap.appendChild(el("h1", { text: opts.title || "Sign in" }));
      wrap.appendChild(el("p", { className: "sh-si-lead", text: opts.lead || "New here? The same steps create your account." }));
      var showGoogle = has(providers, "google");
      var showOidc = has(providers, "oidc");
      if (showGoogle) {
        var g = el("button", { type: "button", className: "sh-si-gbtn" });
        g.innerHTML = GOOGLE_SVG;
        g.appendChild(el("span", { text: "Continue with Google" }));
        g.addEventListener("click", function () {
          if (typeof opts.startProvider === "function") opts.startProvider("google");
        });
        wrap.appendChild(g);
      }
      if (showOidc) {
        var label = opts.oidcLabel ? String(opts.oidcLabel) : "your company account";
        var o = el("button", { type: "button", className: "sh-si-obtn", text: "Continue with " + label });
        if (!showGoogle) o.style.marginTop = "0";
        o.addEventListener("click", function () {
          if (typeof opts.startProvider === "function") opts.startProvider("oidc");
        });
        wrap.appendChild(o);
      }
      if (emailOn && (showGoogle || showOidc)) wrap.appendChild(el("div", { className: "sh-si-or", text: "or" }));
      if (emailOn) {
        wrap.appendChild(el("label", { "for": uid + "-email", text: "Email" }));
        var inp = el("input", {
          className: "sh-si-f" + (fieldErr ? " sh-si-bad" : ""),
          id: uid + "-email",
          type: "email",
          autocomplete: "email",
          placeholder: "you@example.com"
        });
        inp.value = email;
        inp.addEventListener("input", function () { email = inp.value; fieldErr = ""; });
        wrap.appendChild(inp);
        if (fieldErr) {
          var fe = el("p", { className: "sh-si-err", id: uid + "-ferr", role: "alert" });
          fe.innerHTML = INFO_SVG;
          fe.appendChild(el("span", { text: fieldErr }));
          wrap.appendChild(fe);
        }
        var cont = el("button", { type: "submit", className: "sh-si-pbtn", text: "Continue" });
        wrap.appendChild(cont);
      }
      var fine = opts.fine;
      if (fine == null) fine = emailOn ? "We email you a 6-digit code. No password to remember." : "";
      if (fine) wrap.appendChild(el("p", { className: "sh-si-fine", text: fine }));
      if (opts.extra) wrap.appendChild(opts.extra);
      if (startMsg) wrap.appendChild(el("p", { className: "sh-si-warn", role: "alert", text: startMsg }));
      var form = el("form", { novalidate: true });
      form.addEventListener("submit", function (e) {
        e.preventDefault();
        onContinue();
      });
      form.appendChild(wrap);
      return form;
    }

    function renderCode() {
      var wrap = el("div");
      if (opts.top) {
        var top = el("div", { className: "sh-si-top" });
        top.appendChild(opts.top);
        wrap.appendChild(top);
      }
      wrap.appendChild(el("h1", { text: "Check your email" }));
      var lead = el("p", { className: "sh-si-lead" });
      lead.appendChild(document.createTextNode("We sent a 6-digit code to "));
      lead.appendChild(el("b", { text: email }));
      lead.appendChild(document.createTextNode(". "));
      var ch = el("button", { type: "button", className: "sh-si-link", text: "Change", "data-keep": "1" });
      ch.addEventListener("click", function () { goStart(""); });
      lead.appendChild(ch);
      wrap.appendChild(lead);
      wrap.appendChild(el("label", { "for": uid + "-code", text: "Code" }));
      var inp = el("input", {
        className: "sh-si-f sh-si-code" + (codeErr ? " sh-si-bad" : ""),
        id: uid + "-code",
        type: "text",
        inputmode: "numeric",
        autocomplete: "one-time-code",
        enterkeyhint: "done",
        placeholder: "\u00b7\u00b7\u00b7\u00b7\u00b7\u00b7",
        "aria-describedby": uid + "-cerr"
      });
      inp.value = codeVal;
      inp.addEventListener("input", function () { onCodeInput(inp); });
      inp.addEventListener("paste", function (ev) {
        var t = "";
        if (ev.clipboardData) t = ev.clipboardData.getData("text") || "";
        else if (window.clipboardData) t = window.clipboardData.getData("Text") || "";
        if (!t) return;
        ev.preventDefault();
        inp.value = digits(t);
        onCodeInput(inp);
      });
      wrap.appendChild(inp);
      var err = el("p", { className: "sh-si-err", id: uid + "-cerr", role: "alert" });
      if (codeErr) {
        err.innerHTML = INFO_SVG;
        err.appendChild(el("span", { text: codeErr }));
      } else {
        err.style.display = "none";
      }
      wrap.appendChild(err);
      var usedUp = codeReason === "code_used_up";
      var expired = codeReason === "code_expired";
      var primary = el("button", { type: "button", className: "sh-si-pbtn" });
      if (usedUp || expired) {
        primary.textContent = "Send a new code";
        primary.addEventListener("click", sendNew);
      } else {
        primary.textContent = signingIn ? "Signing you in\u2026" : "Sign in";
        if (signingIn) primary.setAttribute("disabled", "");
        primary.addEventListener("click", function () {
          lastSubmitted = "";
          submitCode(inp.value);
        });
      }
      wrap.appendChild(primary);
      var row = el("div", { className: "sh-si-row" });
      var left = el("span", { id: uid + "-resend" });
      if (usedUp) {
        left.appendChild(el("span"));
      } else {
        updateResend(left);
      }
      row.appendChild(left);
      if (isGmail(email)) {
        var a = el("a", { href: gmailURL(), target: "_blank", rel: "noopener noreferrer", text: "Open Gmail" });
        row.appendChild(a);
      }
      wrap.appendChild(row);
      var fine = opts.linkNote
        ? "Not there? Check spam, or wait a minute. The link in the email signs you in too, on this device."
        : "Not there? Check spam, or wait a minute.";
      wrap.appendChild(el("p", { className: "sh-si-fine", text: fine }));
      return wrap;
    }

    function render() {
      if (destroyed) return;
      clear(root);
      if (step === "handle") root.appendChild(renderHandle());
      else if (step === "code") root.appendChild(renderCode());
      else root.appendChild(renderStart());
      if (busy) setBusy(true);
    }

    function askHandle(info) {
      info = info || {};
      if (handleWait) {
        var prev = handleWait;
        handleWait = null;
        prev(null);
      }
      step = "handle";
      handleState = {
        suggestion: info.suggestion || "",
        value: info.suggestion || "",
        address: info.address || "",
        message: info.error || "",
        ok: false
      };
      return new Promise(function (resolve) {
        handleWait = function (v) {
          handleWait = null;
          resolve(v);
        };
        render();
        focusSoon(uid + "-handle");
        var token = ++handleSeq;
        checkHandle(handleState.value, token).then(function (r) {
          if (!r || token !== handleSeq || !handleState) return;
          handleState.ok = r.ok;
          handleState.address = r.address || handleState.address;
          handleState.message = r.message || handleState.message;
          render();
        }).catch(function () {});
      });
    }

    render();
    if (opts.autofocus) {
      if (emailOn) focusSoon(uid + "-email");
      else {
        var first = root.querySelector("button");
        if (first && first.focus) try { first.focus(); } catch (e) {}
      }
    }

    return {
      showStart: function (msg) { goStart(msg || ""); },
      showCode: function (addr) { goCode(addr); },
      showError: function (msg) {
        if (step === "code") {
          codeErr = msg || "";
        } else {
          startMsg = msg || "";
        }
        render();
      },
      setBusy: setBusy,
      askHandle: askHandle,
      destroy: function () {
        destroyed = true;
        stopResend();
        clearTimeout(handleTimer);
        if (handleWait) {
          var d = handleWait;
          handleWait = null;
          d(null);
        }
        if (host && host.parentNode) host.parentNode.removeChild(host);
      }
    };
  }

  window.SHSignIn = {
    mount: mount,
    message: function (err) { return message(err, "Simple Host"); },
    isGmail: isGmail,
    gmailURL: gmailURL
  };
})();
