import {
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import {
  motion,
  useReducedMotion,
  type Transition,
  type Variants,
} from "motion/react";

import { appRoutes } from "@/app/route-config";

const routeTransition: Transition = {
  duration: 0.16,
  ease: [0.22, 1, 0.36, 1],
};

const reducedRouteTransition: Transition = {
  duration: 0.01,
};

const routeVariants: Variants = {
  initial: {
    opacity: 0,
    y: 6,
  },
  animate: {
    opacity: 1,
    y: 0,
  },
};

const reducedRouteVariants: Variants = {
  initial: { opacity: 0 },
  animate: { opacity: 1 },
};

export function AnimatedRoutes() {
  const location = useLocation();
  const prefersReducedMotion = useReducedMotion();

  return (
    <motion.div
      key={location.pathname}
      variants={prefersReducedMotion ? reducedRouteVariants : routeVariants}
      initial="initial"
      animate="animate"
      transition={prefersReducedMotion ? reducedRouteTransition : routeTransition}
      className="min-w-0"
    >
      <Routes location={location}>
        {appRoutes.map((route) => (
          <Route key={route.path} path={route.path} element={route.element} />
        ))}
      </Routes>
    </motion.div>
  );
}
