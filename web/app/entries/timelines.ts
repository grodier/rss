// The Timelines page's and the timeline form's enhancements. Their
// stylesheet is settings.css, which the other pages that manage things
// share. Every page works without them.
import "../styles/settings.css";
import * as backForward from "../features/back-forward";
import * as reorder from "../features/reorder";
import { setupRUM } from "../features/rum";
import * as timelineForm from "../features/timeline-form";

setupRUM(); // first, so a setup that throws can't stop the page being measured
backForward.init();
timelineForm.init();
reorder.init();
