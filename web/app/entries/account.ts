// The enhancements of the Settings pages that have any: password fields,
// imports and their progress. Their stylesheet is settings.css, which the
// other pages that manage things share. Every page works without them.
import "../styles/settings.css";
import * as busyForms from "../../shared/lib/busy-forms";
import { localizeTimes } from "../../shared/lib/local-times";
import * as passwordToggle from "../../shared/lib/password-toggle";
import * as backForward from "../features/back-forward";
import * as importStatus from "../features/import-status";

localizeTimes(document);
backForward.init();
busyForms.init();
importStatus.init();
passwordToggle.init();
