import { Link } from "react-router-dom";
import { Button } from "@/shared/ui/button";

export function NotFoundPage() {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-4 px-4 text-center">
      <p className="font-mono text-sm text-gray-500">404</p>
      <h1 className="text-2xl font-semibold">Page not found</h1>
      <p className="text-sm text-gray-500">
        The page you are looking for does not exist or was moved.
      </p>
      <Button asChild>
        <Link to="/">Back to dashboard</Link>
      </Button>
    </div>
  );
}
