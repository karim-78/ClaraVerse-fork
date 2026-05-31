import { Button } from '@/components/ui';

export const Home = () => {
  return (
    <div className="space-y-6">
      <h1 className="text-4xl font-bold text-gray-900">Welcome to ClaraVerse</h1>
      <p className="text-lg text-gray-600">
        A production-ready React application built with TypeScript, Vite, and Tailwind CSS.
      </p>
      <div className="flex gap-4">
        <Button variant="primary">Get Started</Button>
        <Button variant="outline">Learn More</Button>
      </div>
    </div>
  );
};
