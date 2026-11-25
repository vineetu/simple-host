// Notifications: browser push on the devices the owner switches on. The
// site-wide service worker (/sw.js, which loads /push-sw.js) shows them, so
// nothing else is registered here. What each site tells is in its Alerts tab.
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { FieldMessage } from "@/components/ui/input";
import { useToast } from "@/components/ui/toast";
import { pushKey, type Me } from "../data";
import { Part, Row } from "../bits";

const canPush = () => typeof navigator !== "undefined" && "serviceWorker" in navigator && "PushManager" in window;

function deviceName() {
  const ua = navigator.userAgent;
  const os = /iPhone|iPad/.test(ua) ? "iPhone or iPad" : /Android/.test(ua) ? "Android" : /Mac/.test(ua) ? "Mac" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  return browser + (os ? " on " + os : "");
}

export function Notifications({ me }: { me: Me }) {
  const call = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const supported = canPush();

  const turnOn = async () => {
    setBusy(true);
    setError("");
    try {
      if ((await Notification.requestPermission()) !== "granted") throw new Error("This browser is set to block notifications from Simple Host. Allow them in its site settings, then try again.");
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: pushKey(me.push_key) });
      const j = sub.toJSON();
      await call("POST", "/v1/me/push", { endpoint: j.endpoint, keys: j.keys, name: deviceName() });
      await qc.invalidateQueries({ queryKey: ["me"] });
      toast("Notifications are on for this device.");
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Part
        title="Notifications on your devices"
        note="A push on this phone or computer when something you chose happens on a site: someone asks to open it, or new data arrives. Choose what in each site's Alerts tab."
        className="mt-2"
      >
        {me.devices.length ? (
          <ul className="m-0 mb-4 list-none p-0" aria-label="Devices with notifications on">
            {me.devices.map((d) => (
              <Row
                key={d.id}
                end={
                  <Button
                    variant="ghost"
                    className="px-3 text-bad"
                    onClick={async () => {
                      try {
                        await call("DELETE", "/v1/me/push/" + encodeURIComponent(d.id));
                        await qc.invalidateQueries({ queryKey: ["me"] });
                        toast("No more notifications on " + (d.name || "that device") + ".");
                      } catch (e) {
                        toast((e as Error).message, { bad: true });
                      }
                    }}
                  >
                    Turn off
                  </Button>
                }
              >
                {d.name || "A browser"}
              </Row>
            ))}
          </ul>
        ) : (
          <p className="mb-4 text-ink-soft">No devices yet.</p>
        )}
        {supported && me.push_key ? (
          <Button variant="primary" className="px-6" onClick={turnOn} disabled={busy}>
            Turn on for this device
          </Button>
        ) : (
          <p className="text-sm text-ink-soft">
            {me.push_key
              ? "This browser cannot show notifications. On an iPhone, add Simple Host to the Home Screen first, then open it from there."
              : "This install of Simple Host does not send notifications."}
          </p>
        )}
        {error && <FieldMessage tone="bad">{error}</FieldMessage>}
      </Part>
    </>
  );
}
