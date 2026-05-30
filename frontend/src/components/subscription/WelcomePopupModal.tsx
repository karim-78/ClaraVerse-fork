import { Modal } from '@/components/design-system/feedback/Modal/Modal';
import { Button } from '@/components/design-system/Button/Button';
import { useSubscriptionStore } from '@/store/useSubscriptionStore';
import { userService } from '@/services/api';
import { useState, useMemo } from 'react';

// Privacy quotes pool (randomly selected)
const PRIVACY_QUOTES = [
  "Privacy is not about hiding; it's about control over your own information.",
  'Your data is yours. We encrypt everything so only you can access it.',
  'We believe in privacy by design, not privacy as an afterthought.',
  "Encryption isn't just a feature—it's your right to digital privacy.",
  'Local-first means your data stays with you, always.',
  'Privacy is a fundamental human right, not a premium feature.',
  "We don't sell your data because it's not ours to sell.",
  'End-to-end encryption: What happens in your chat, stays in your chat.',
];

export function WelcomePopupModal() {
  const { subscription, fetchSubscription } = useSubscriptionStore();
  const [isClosing, setIsClosing] = useState(false);

  // Debug logging
  console.log('WelcomePopupModal - Subscription data:', {
    tier: subscription?.tier,
    is_promo_user: subscription?.is_promo_user,
    has_seen_welcome_popup: subscription?.has_seen_welcome_popup,
    subscription_expires_at: subscription?.subscription_expires_at,
    dodo_subscription_id: subscription?.dodo_subscription_id,
  });

  // Only show for promo users who haven't seen it
  // Disabled - keeping code intact but hiding the popup
  const shouldShow = false;
  const _originalShouldShow =
    subscription?.is_promo_user === true && subscription?.has_seen_welcome_popup === false;

  // Random privacy quote (memoized to prevent re-rendering changes)
  const randomQuote = useMemo(() => {
    return PRIVACY_QUOTES[Math.floor(Math.random() * PRIVACY_QUOTES.length)];
  }, []);

  // Calculate days remaining
  const daysRemaining = useMemo(() => {
    if (!subscription?.subscription_expires_at) return 30;

    const expiryDate = new Date(subscription.subscription_expires_at);
    const now = new Date();
    const diffMs = expiryDate.getTime() - now.getTime();
    const diffDays = Math.ceil(diffMs / (1000 * 60 * 60 * 24));

    return Math.max(0, diffDays);
  }, [subscription?.subscription_expires_at]);

  const handleDismiss = async () => {
    setIsClosing(true);
    try {
      await userService.markWelcomePopupSeen();
      // Refresh subscription to update the flag in store
      await fetchSubscription();
    } catch (error) {
      console.error('Failed to mark welcome popup as seen:', error);
      // Still close the modal even if API call fails
    }
    setIsClosing(false);
  };

  if (!shouldShow) return null;

  return (
    <Modal
      isOpen={true}
      onClose={handleDismiss}
      size="lg"
      closeOnBackdrop={false}
      closeOnEscape={false}
      showClose={false}
      className="!rounded-xl overflow-hidden [&_.modal-body]:!p-0"
    >
      <div className="flex flex-col md:flex-row overflow-hidden rounded-xl">
        {/* Left side - Image (edge to edge) */}
        <div className="md:w-1/2 flex-shrink-0 overflow-hidden">
          <img
            src="/clara-welcome.png"
            alt="Welcome"
            className="w-full h-full object-cover rounded-l-xl"
            style={{ minHeight: '500px', maxHeight: '500px' }}
          />
        </div>

        {/* Right side - Content */}
        <div className="md:w-1/2 p-10 flex flex-col justify-between">
          <div className="space-y-6">
            {/* Welcome Message */}
            <div>
              <h2 className="text-2xl font-bold mb-3 text-gray-900 dark:text-gray-100">
                Welcome Onboard!
              </h2>
              <p className="text-base text-gray-700 dark:text-gray-300">
                Enjoy <strong className="text-blue-600 dark:text-blue-400">Dobby PRO</strong> for
                the next <strong>{daysRemaining} days</strong>
              </p>
            </div>

            {/* Privacy Quote */}
            <div className="border-l-4 border-purple-500 pl-4 py-2">
              <p className="text-sm text-gray-600 dark:text-gray-400 italic">
                &quot;{randomQuote}&quot;
              </p>
            </div>
          </div>

          {/* Action Button */}
          <div>
            <Button
              variant="primary"
              size="lg"
              fullWidth
              onClick={handleDismiss}
              disabled={isClosing}
            >
              {isClosing ? 'Getting Started...' : 'Get Started'}
            </Button>
          </div>
        </div>
      </div>
    </Modal>
  );
}
