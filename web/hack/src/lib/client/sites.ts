// Sites: the zip upload for the event's or a team's site (bytes, PUT with ?owner=&create=1).
import { seg } from "./_e";
export const uploadPath = (site: string, owner: string) => "/v1/sites/" + seg(site) + "?owner=" + seg(owner) + "&create=1";
