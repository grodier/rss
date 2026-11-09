// Log in's, Sign up's and Reset password's enhancements. Their stylesheet
// is solo.css, which the other logged-out pages share. Every page works
// without them.
import "../styles/solo.css";
import * as busyForms from "../../shared/lib/busy-forms";
import * as passwordToggle from "../../shared/lib/password-toggle";
import * as backForward from "../features/back-forward";
import { setupRUM } from "../features/rum";

setupRUM(); // first, so a setup that throws can't stop the page being measured
backForward.init();
busyForms.init();
passwordToggle.init();
