// The logged-out, policy and error pages, and shared timelines, which have
// no enhancement of their own (Log in's, Sign up's, Reset password's and the
// landing page's are login's). Their stylesheet is solo.css, by the group's
// name. Every page works without this.
import { setupRUM } from "../features/rum";

setupRUM();
